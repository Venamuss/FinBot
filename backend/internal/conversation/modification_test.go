package conversation

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNullableModificationArguments(t *testing.T) {
	for _, test := range []struct{ name, args string }{
		{"update_expense", `{"id":null,"query":null,"previous_amount":null,"amount":"450","category":null,"spent_at":null,"comment":null,"currency":null,"request_context":null}`},
		{"delete_expense", `{"id":null,"query":"такси","request_context":null}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			handle(t, orchestrator, model, 1, 101, "такси 350", "create_expense", `{"amount":"350","category":"транспорт","spent_at":"yesterday","comment":"такси"}`)
			before := store.repo.items[1]
			reply := handle(t, orchestrator, model, 2, 101, "synthetic modification", test.name, test.args)
			if test.name == "delete_expense" {
				if len(store.repo.items) != 0 || !strings.Contains(reply, "Расход удалён") {
					t.Fatal("nullable delete was rejected")
				}
				return
			}
			after := store.repo.items[1]
			if after.Amount.String() != "450.00" || !strings.Contains(reply, "Расход обновлён") {
				t.Fatal("nullable update was rejected")
			}
			before.Amount = after.Amount
			before.UpdatedAt = after.UpdatedAt
			if !reflect.DeepEqual(before, after) {
				t.Fatal("amount update changed unrelated fields")
			}
		})
	}
}

func TestModificationSchemasAndCommentClear(t *testing.T) {
	for _, definition := range toolsForModel() {
		if definition.Function.Name != "update_expense" && definition.Function.Name != "delete_expense" {
			continue
		}
		var schema struct {
			Required   []string `json:"required"`
			Properties map[string]struct {
				Type []string `json:"type"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(definition.Function.Parameters, &schema); err != nil {
			t.Fatal(err)
		}
		if !definition.Function.Strict || len(schema.Required) != len(schema.Properties) {
			t.Fatal("modification schema is not strict")
		}
		for _, property := range schema.Properties {
			if !contains(property.Type, "null") {
				t.Fatal("unused modification property cannot be null")
			}
		}
	}
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "кофе 350", "create_expense", `{"amount":"350","spent_at":"today","comment":"кофе"}`)
	handle(t, orchestrator, model, 2, 101, "очисти комментарий", "update_expense", `{"comment":""}`)
	if store.repo.items[1].Comment != nil || store.repo.items[1].Amount.String() != "350.00" {
		t.Fatal("explicit comment clear failed")
	}
	reply := handle(t, orchestrator, model, 3, 101, "synthetic", "update_expense", `{"amount":null,"category":null,"spent_at":null,"comment":null}`)
	if reply != invalidRequest {
		t.Fatal("empty patch must be rejected")
	}
}

func TestIndependentModificationsReplacePendingStates(t *testing.T) {
	for _, pendingType := range []string{"amount", "currency", "selection", "confirmation", "target"} {
		for _, name := range []string{"update_expense", "delete_expense"} {
			t.Run(pendingType+"/"+name, func(t *testing.T) {
				orchestrator, model, store := setup(t)
				for id := int64(1); id <= 2; id++ {
					handle(t, orchestrator, model, id, 101, "такси 350", "create_expense", `{"amount":"350","spent_at":"today","comment":"такси"}`)
				}
				switch pendingType {
				case "amount":
					handle(t, orchestrator, model, 3, 101, "кофе", "clarify", `{"field":"amount","comment":"кофе"}`)
				case "currency":
					handle(t, orchestrator, model, 3, 101, "кофе 5 USD", "create_expense", `{"amount":"5","spent_at":"today","comment":"кофе"}`)
				case "selection":
					handle(t, orchestrator, model, 3, 101, "удали такси", "delete_expense", `{"query":"такси"}`)
				case "confirmation":
					handle(t, orchestrator, model, 3, 101, "последнее было не 100, а 450", "update_expense", `{"previous_amount":"100","amount":"450"}`)
				case "target":
					handle(t, orchestrator, model, 3, 101, "удали трату", "clarify", `{"field":"target","intent":"delete_expense"}`)
				}
				args := `{"id":"1","request_context":"new"}`
				if name == "update_expense" {
					args = `{"id":"1","amount":"700","request_context":"new"}`
				}
				handle(t, orchestrator, model, 4, 101, "independent command", name, args)
				if orchestrator.states[101].pending != nil || store.repo.items[2].Amount.String() != "350.00" {
					t.Fatal("independent command kept a stale request or changed wrong expense")
				}
				item, found := store.repo.items[1]
				if name == "update_expense" && (!found || item.Amount.String() != "700.00") {
					t.Fatal("independent update was blocked")
				}
				if name == "delete_expense" && found {
					t.Fatal("independent delete was blocked")
				}
			})
		}
	}
}

func TestTargetClarificationRetainsModification(t *testing.T) {
	for _, name := range []string{"update_expense", "delete_expense"} {
		t.Run(name, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			handle(t, orchestrator, model, 1, 101, "кофе 350", "create_expense", `{"amount":"350","spent_at":"today","comment":"кофе"}`)
			args := `{"field":"target","intent":"delete_expense","query":"старое описание"}`
			if name == "update_expense" {
				args = `{"field":"target","intent":"update_expense","query":"старое описание","amount":"450"}`
			}
			handle(t, orchestrator, model, 2, 101, "уточняемый запрос", "clarify", args)
			answer := `{"id":"1","request_context":"followup"}`
			if name == "update_expense" {
				answer = `{"id":"1","category":"жильё","amount":"999","request_context":"followup"}`
			}
			reply := handle(t, orchestrator, model, 3, 101, "кофе", name, answer)
			if reply == invalidRequest || orchestrator.states[101].pending != nil {
				t.Fatal("target answer was rejected")
			}
			if name == "update_expense" && (store.repo.items[1].Amount.String() != "450.00" || store.repo.items[1].Category != "другое") {
				t.Fatal("target answer lost patch or injected an unrelated change")
			}
			if name == "delete_expense" && len(store.repo.items) != 0 {
				t.Fatal("delete target answer failed")
			}
		})
	}
}

func TestLatestModificationUsesCreationOrderAndOwner(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "кофе 350", "create_expense", `{"amount":"350","spent_at":"today","comment":"кофе"}`)
	handle(t, orchestrator, model, 2, 101, "такси 500 вчера", "create_expense", `{"amount":"500","spent_at":"yesterday","comment":"такси"}`)
	handle(t, orchestrator, model, 3, 202, "кофе 900", "create_expense", `{"amount":"900","spent_at":"today","comment":"кофе"}`)
	delete(orchestrator.states, 101)
	handle(t, orchestrator, model, 4, 101, "последнее было 450", "update_expense", `{"amount":"450"}`)
	if store.repo.items[2].Amount.String() != "450.00" || store.repo.items[1].Amount.String() != "350.00" || store.repo.items[3].Amount.String() != "900.00" {
		t.Fatal("latest resolution used wrong order or owner")
	}
	delete(orchestrator.states, 101)
	handle(t, orchestrator, model, 5, 101, "убери последнюю запись", "delete_expense", `{}`)
	if _, found := store.repo.items[2]; found || len(store.repo.items) != 2 {
		t.Fatal("latest deletion used wrong order or owner")
	}
}

func TestFailedModificationDoesNotReplaceCommittedState(t *testing.T) {
	orchestrator, model, store := setup(t)
	now := time.Now()
	orchestrator.now = func() time.Time {
		return now
	}
	handle(t, orchestrator, model, 1, 101, "кофе 350", "create_expense", `{"amount":"350","spent_at":"today","comment":"кофе"}`)
	before := orchestrator.states[101]
	store.fail = true
	model.response = tool("delete_expense", `{}`)
	reply, err := orchestrator.Handle(context.Background(), 2, 101, "удали последнюю трату", time.Now())
	if err == nil || reply != "" || !reflect.DeepEqual(before, orchestrator.states[101]) || len(store.repo.items) != 1 {
		t.Fatal("failed transaction changed state or confirmed success")
	}
}
