package conversation

import (
	"reflect"
	"strings"
	"testing"
)

// The model must actually propose a valid expense tool: a parser error or an
// early input filter would not reproduce the original tool-selection bug.
func TestUnclearInputCannotExecuteProposedTools(t *testing.T) {
	for _, text := range []string{"ыфваываы", "лвлала", "asdfghjkl", "???!!!...", "ыфв@asd%жж", "привет", "кофе"} {
		for _, proposed := range []struct{ name, args string }{
			{"list_expenses", `{}`},
			{"total_expenses", `{}`},
			{"navigate_expenses", `{"direction":"next"}`},
			{"create_expense", `{"amount":"350","spent_at":"today"}`},
			{"update_expense", `{"amount":"450"}`},
			{"delete_expense", `{}`},
		} {
			t.Run(text+"/"+proposed.name, func(t *testing.T) {
				orchestrator, model, store := setup(t)
				handle(t, orchestrator, model, 1, 101, "кофе 350", "create_expense", `{"amount":"350","spent_at":"today","comment":"кофе"}`)
				before := store.repo.items[1]
				reads, writes := store.repo.expenseReads, store.repo.expenseWrites
				calls := model.calls
				reply := handle(t, orchestrator, model, 2, 101, text, proposed.name, proposed.args)
				if model.calls != calls+1 {
					t.Fatal("test did not exercise model tool selection")
				}
				if store.repo.expenseReads != reads || store.repo.expenseWrites != writes || !reflect.DeepEqual(store.repo.items[1], before) || store.repo.sequence != 1 {
					t.Fatalf("unclear input executed an expense action: %s", reply)
				}
				if proposed.name == "list_expenses" || proposed.name == "total_expenses" || proposed.name == "navigate_expenses" {
					if !strings.HasPrefix(reply, "Не понял запрос.") || orchestrator.states[101].listing != nil {
						t.Fatalf("expected unknown intent without listing state, got %s", reply)
					}
				}
			})
		}
	}
}

func TestUnknownIntentAvailableInEveryState(t *testing.T) {
	for _, field := range []string{"", "amount", "category", "spent_at", "currency", "target", "selection", "confirmation", "delete_confirmation"} {
		t.Run(field, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			current := state{}
			if field != "" {
				current.pending = &pending{field: field, action: action{name: "create_expense"}}
			}
			current.touched = orchestrator.now()
			orchestrator.states[101] = current
			reply := handle(t, orchestrator, model, 1, 101, "ыфваываы", "unknown_intent", `{}`)
			if reply != unclearIntent(current.pending) || orchestrator.states[101].pending != current.pending || len(store.repo.items) != 0 {
				t.Fatal("unknown intent altered pending work:", reply)
			}
			available := false
			for _, definition := range model.request.Tools {
				if definition.Function.Name == "unknown_intent" {
					available = true
				}
			}
			if !available {
				t.Fatal("model has no unknown-intent outcome")
			}
		})
	}
}

func TestReadIntentNaturalLanguageAndTypos(t *testing.T) {
	for _, text := range []string{
		"покажи мои расходы", "на что я тратил деньги?", "сколько потратил за неделю?",
		"пакажи мои траты", "покажи расхды", "мои расхдоы", "скока потратил", "глянь расходы плиз",
	} {
		t.Run(text, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			seedListingExpenses(t, store, 101, "2026-10-17")
			name := "list_expenses"
			if strings.Contains(text, "сколько") || strings.Contains(text, "скока") {
				name = "total_expenses"
			}
			reply := handle(t, orchestrator, model, 1, 101, text, name, `{}`)
			if store.repo.listCalls != 1 || strings.Contains(reply, "Не понял") {
				t.Fatal("valid read rejected:", reply)
			}
		})
	}
	for _, description := range []string{"кофе", "коффе", "ламборджини", "блёрф", "SSD 500GB"} {
		t.Run(description, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			handle(t, orchestrator, model, 1, 101, description+" 350", "create_expense", `{"amount":"350","spent_at":"today"}`)
			if len(store.repo.items) != 1 || store.repo.items[1].Amount.String() != "350.00" {
				t.Fatal("description vocabulary restricted valid creation")
			}
		})
	}
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "блёрф", "clarify", `{"field":"amount","comment":"блёрф"}`)
	handle(t, orchestrator, model, 2, 101, "2", "answer_clarification", `{"value":"2"}`)
	if len(store.repo.items) != 1 || *store.repo.items[1].Comment != "блёрф" {
		t.Fatal("unfamiliar single-word description lost its amount followup")
	}
}

func TestUnclearPendingReplyCannotCompleteDraft(t *testing.T) {
	for _, field := range []string{"amount", "category", "spent_at"} {
		for _, proposed := range []string{"list_expenses", "answer_clarification", "create_expense", "unknown_intent"} {
			t.Run(field+"/"+proposed, func(t *testing.T) {
				orchestrator, model, store := setup(t)
				args := `{"field":"` + field + `","intent":"create_expense","comment":"кофе"}`
				if field != "amount" {
					args = `{"field":"` + field + `","intent":"update_expense"}`
					handle(t, orchestrator, model, 1, 101, "кофе 350", "create_expense", `{"amount":"350","spent_at":"today"}`)
				}
				text := "кофе"
				if field != "amount" {
					text = "измени " + map[string]string{"category": "категорию", "spent_at": "дату"}[field] + " последней траты"
				}
				handle(t, orchestrator, model, 2, 101, text, "clarify", args)
				pending := orchestrator.states[101].pending
				before := store.repo.items[1]
				value := map[string]string{"amount": "350", "category": "еда", "spent_at": "today"}[field]
				args = `{}`
				if proposed == "answer_clarification" {
					args = `{"value":"` + value + `"}`
				} else if proposed == "create_expense" {
					args = `{"amount":"350","spent_at":"today","request_context":"followup"}`
				}
				handle(t, orchestrator, model, 3, 101, "ыфваываы", proposed, args)
				if orchestrator.states[101].pending != pending || !reflect.DeepEqual(store.repo.items[1], before) || store.repo.listCalls != 0 || store.repo.totalCalls != 0 {
					t.Fatal("unclear reply changed draft, read or mutated expenses")
				}
				handle(t, orchestrator, model, 4, 101, "стоп", "list_expenses", `{}`)
				if orchestrator.states[101].pending != nil {
					t.Fatal("cancellation after unclear reply failed")
				}
			})
		}
	}
}

func TestUnclearReplyPreservesSelectionConfirmationAndPagination(t *testing.T) {
	orchestrator, model, store := setup(t)
	for id := int64(1); id <= 2; id++ {
		handle(t, orchestrator, model, id, 101, "такси 350", "create_expense", `{"amount":"350","spent_at":"today","comment":"такси"}`)
	}
	handle(t, orchestrator, model, 3, 101, "мои расходы", "list_expenses", `{"limit":"1"}`)
	before := orchestrator.states[101]
	handle(t, orchestrator, model, 4, 101, "asdfghjkl", "navigate_expenses", `{"direction":"next"}`)
	if orchestrator.states[101].listing != before.listing || orchestrator.states[101].focus != before.focus || store.repo.listCalls != 1 {
		t.Fatal("unclear input changed page/focus or queried expenses")
	}
	handle(t, orchestrator, model, 5, 101, "ещё", "navigate_expenses", `{"direction":"next"}`)
	handle(t, orchestrator, model, 6, 101, "удали такси", "delete_expense", `{"query":"такси"}`)
	pending := orchestrator.states[101].pending
	handle(t, orchestrator, model, 7, 101, "???!!!", "choose_expense", `{"choice":"2"}`)
	if orchestrator.states[101].pending != pending || len(store.repo.items) != 2 {
		t.Fatal("unclear input selected/deleted an expense")
	}
	handle(t, orchestrator, model, 8, 101, "2", "choose_expense", `{"choice":"2"}`)
	pending = orchestrator.states[101].pending
	if pending == nil || pending.field != "delete_confirmation" {
		t.Fatal("numeric selection no longer opens confirmation")
	}
	handle(t, orchestrator, model, 9, 101, "ыфваываы", "list_expenses", `{}`)
	handle(t, orchestrator, model, 10, 101, "да ыфваываы", "choose_expense", `{"choice":"1"}`)
	if orchestrator.states[101].pending != pending || len(store.repo.items) != 2 {
		t.Fatal("unclear input confirmed or discarded a deletion")
	}
	handle(t, orchestrator, model, 11, 101, "нет", "delete_expense", `{}`)
	handle(t, orchestrator, model, 12, 101, "да", "delete_expense", `{}`)
	if len(store.repo.items) != 2 || orchestrator.states[101].pending != nil {
		t.Fatal("declined deletion was rearmed")
	}
	handle(t, orchestrator, model, 13, 101, "удали последнюю", "delete_expense", `{}`)
	handle(t, orchestrator, model, 14, 101, "ыфваываы", "unknown_intent", `{}`)
	handle(t, orchestrator, model, 15, 101, "да", "delete_expense", `{}`)
	if len(store.repo.items) != 1 {
		t.Fatal("explicit confirmation after unclear reply failed")
	}
}

func TestUnclearDateReplyCannotCreateFromEarlierAmount(t *testing.T) {
	for _, proposed := range []struct{ name, args string }{
		{"answer_clarification", `{"value":"today"}`},
		{"create_expense", `{"amount":"350","spent_at":"today","request_context":"followup"}`},
		{"clarify", `{"field":"category","intent":"create_expense","amount":"350","spent_at":"today"}`},
	} {
		t.Run(proposed.name, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			handle(t, orchestrator, model, 1, 101, "кофе 350 дату уточню", "clarify", `{"field":"spent_at","intent":"create_expense","amount":"350","comment":"кофе"}`)
			pending := orchestrator.states[101].pending
			reply := handle(t, orchestrator, model, 2, 101, "asdfghjkl", proposed.name, proposed.args)
			if reply != unclearIntent(pending) || len(store.repo.items) != 0 || orchestrator.states[101].pending != pending {
				t.Fatal("meaningless date reply completed a creation:", reply)
			}
			reply = handle(t, orchestrator, model, 3, 101, "сегодня", "answer_clarification", `{"value":"today"}`)
			if len(store.repo.items) != 1 || store.repo.items[1].Amount.String() != "350.00" {
				t.Fatal("valid date reply no longer completes creation:", reply)
			}
		})
	}
}
