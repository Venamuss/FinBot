package conversation

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/llm"
)

func TestAuditUniqueDeleteRequiresConfirmation(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "такси 300", "create_expense", `{"amount":"300","spent_at":"today","comment":"такси"}`)
	before := store.repo.items[1]
	reply := handle(t, orchestrator, model, 2, 101, "удали такси", "delete_expense", `{"query":"такси"}`)
	if !reflect.DeepEqual(before, store.repo.items[1]) || !strings.Contains(reply, "да или нет") {
		t.Fatal("delete request mutated a record without confirmation")
	}
}

func TestAuditReadOnlyCannotBecomeMutation(t *testing.T) {
	for _, name := range []string{"create_expense", "update_expense", "delete_expense"} {
		t.Run(name, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			handle(t, orchestrator, model, 1, 101, "такси 300", "create_expense", `{"amount":"300","spent_at":"today","comment":"такси"}`)
			before := store.repo.items[1]
			args := `{"amount":"900","spent_at":"today","comment":"такси"}`
			if name == "delete_expense" {
				args = `{}`
			}
			handle(t, orchestrator, model, 2, 101, "мои расходы", name, args)
			if len(store.repo.items) != 1 || !reflect.DeepEqual(before, store.repo.items[1]) {
				t.Fatal("read request executed a model-selected write")
			}
		})
	}
}

func TestAuditUnrelatedTextCannotChooseExpense(t *testing.T) {
	orchestrator, model, store := setup(t)
	for id := int64(1); id <= 2; id++ {
		handle(t, orchestrator, model, id, 101, "такси 300", "create_expense", `{"amount":"300","spent_at":"today","comment":"такси"}`)
	}
	handle(t, orchestrator, model, 3, 101, "измени такси на 600", "update_expense", `{"query":"такси","amount":"600"}`)
	before := store.repo.items[2]
	handle(t, orchestrator, model, 4, 101, "привет", "choose_expense", `{"choice":"1"}`)
	if !reflect.DeepEqual(before, store.repo.items[2]) {
		t.Fatal("unrelated text confirmed a deferred update")
	}
}

func TestAuditCreationCannotInventAmount(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "кофе", "create_expense", `{"amount":"300","spent_at":"today","comment":"кофе"}`)
	if len(store.repo.items) != 0 {
		t.Fatal("model invented a missing monetary value")
	}
}

func TestAuditYesWithoutPendingCannotMutate(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "такси 300", "create_expense", `{"amount":"300","spent_at":"today","comment":"такси"}`)
	before := store.repo.items[1]
	handle(t, orchestrator, model, 2, 101, "да", "update_expense", `{"amount":"900"}`)
	if !reflect.DeepEqual(before, store.repo.items[1]) {
		t.Fatal("idle yes executed a mutation")
	}
}

func TestAuditConfirmationDeadlineDoesNotSlide(t *testing.T) {
	orchestrator, model, store := setup(t)
	now := time.Now()
	orchestrator.now = func() time.Time {
		return now
	}
	handle(t, orchestrator, model, 1, 101, "такси 300", "create_expense", `{"amount":"300","spent_at":"today","comment":"такси"}`)
	handle(t, orchestrator, model, 2, 101, "удали такси", "delete_expense", `{"query":"такси"}`)
	deadline := orchestrator.states[101].pending.expiresAt
	now = now.Add(29 * time.Minute)
	handle(t, orchestrator, model, 3, 101, "привет", "choose_expense", `{"choice":"1"}`)
	if orchestrator.states[101].pending.expiresAt != deadline {
		t.Fatal("unrelated reply extended confirmation deadline")
	}
	now = now.Add(2 * time.Minute)
	handle(t, orchestrator, model, 4, 101, "да", "delete_expense", `{}`)
	if len(store.repo.items) != 1 || orchestrator.states[101].pending != nil {
		t.Fatal("expired confirmation deleted an expense")
	}
}

type auditModelFunc func(context.Context, llm.Request) (llm.Response, error)

func (model auditModelFunc) Complete(ctx context.Context, request llm.Request) (llm.Response, error) {
	return model(ctx, request)
}

func TestAuditPendingExpiresDuringPlanning(t *testing.T) {
	orchestrator, model, store := setup(t)
	now := time.Now()
	orchestrator.now = func() time.Time {
		return now
	}
	handle(t, orchestrator, model, 1, 101, "такси 300", "create_expense", `{"amount":"300","spent_at":"today","comment":"такси"}`)
	handle(t, orchestrator, model, 2, 101, "последнее было не 200, а 450", "update_expense", `{"previous_amount":"200","amount":"450"}`)
	now = now.Add(29 * time.Minute)
	orchestrator.model = auditModelFunc(func(context.Context, llm.Request) (llm.Response, error) {
		now = now.Add(2 * time.Minute)
		return tool("choose_expense", `{"choice":"1"}`), nil
	})
	if _, err := orchestrator.Handle(context.Background(), 3, 101, "1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if store.repo.items[1].Amount.String() != "300.00" || orchestrator.states[101].pending != nil {
		t.Fatal("state expired during planning but its update executed")
	}
}

func TestAuditConcurrentMessagesAndDeduplication(t *testing.T) {
	orchestrator, model, store := setup(t)
	model.response = tool("create_expense", `{"amount":"300","spent_at":"today","comment":"такси"}`)
	var workers sync.WaitGroup
	for index := int64(1); index <= 20; index++ {
		for range 2 {
			workers.Go(func() {
				owner := int64(101)
				if index%2 == 0 {
					owner = 202
				}
				if _, err := orchestrator.Handle(context.Background(), index, owner, "такси 300", time.Now()); err != nil {
					t.Error(err)
				}
			})
		}
	}
	workers.Wait()
	if len(store.repo.items) != 20 || model.calls != 20 || len(store.results) != 20 {
		t.Fatal("concurrent updates were lost, duplicated or planned twice")
	}
	if orchestrator.states[101].focus.UserID != 101 || orchestrator.states[202].focus.UserID != 202 {
		t.Fatal("concurrent owners shared focus")
	}
}

func TestAuditUnrequestedUpdateFieldsAreRejected(t *testing.T) {
	for _, args := range []string{
		`{"amount":"600","category":"жильё"}`,
		`{"amount":"600","spent_at":"yesterday"}`,
		`{"amount":"600","comment":"такси"}`,
		`{"amount":"900"}`,
	} {
		t.Run(args, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			handle(t, orchestrator, model, 1, 101, "такси 300", "create_expense", `{"amount":"300","spent_at":"today","comment":"такси"}`)
			before := store.repo.items[1]
			handle(t, orchestrator, model, 2, 101, "измени сумму такси на 600", "update_expense", args)
			if !reflect.DeepEqual(before, store.repo.items[1]) {
				t.Fatal("model added an unrequested field or monetary value")
			}
		})
	}
}

func TestAuditExplicitIDCannotBeReplacedByModel(t *testing.T) {
	orchestrator, model, store := setup(t)
	for id := int64(1); id <= 2; id++ {
		handle(t, orchestrator, model, id, 101, "такси 300", "create_expense", `{"amount":"300","spent_at":"today","comment":"такси"}`)
	}
	before := store.repo.items[2]
	handle(t, orchestrator, model, 3, 101, "измени расход #1 на 600", "update_expense", `{"id":"2","amount":"600"}`)
	if !reflect.DeepEqual(before, store.repo.items[2]) {
		t.Fatal("model changed the explicitly specified expense ID")
	}
}

func TestAuditCompleteUpdateSupersedesConfirmationWithoutModelContextFlag(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "такси 300", "create_expense", `{"amount":"300","spent_at":"today","comment":"такси"}`)
	handle(t, orchestrator, model, 2, 101, "удали такси", "delete_expense", `{"query":"такси"}`)
	handle(t, orchestrator, model, 3, 101, "измени сумму такси на 600", "update_expense", `{"query":"такси","amount":"600"}`)
	if store.repo.items[1].Amount.String() != "600.00" || orchestrator.states[101].pending != nil {
		t.Fatal("complete independent update was trapped in a deletion confirmation")
	}
}

func TestAuditMissingDeleteReferenceCannotUseFocus(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "такси 300", "create_expense", `{"amount":"300","spent_at":"today","comment":"такси"}`)
	handle(t, orchestrator, model, 2, 101, "удали", "delete_expense", `{}`)
	if len(store.repo.items) != 1 || orchestrator.states[101].stateType() != "awaiting_target" {
		t.Fatal("missing delete reference silently selected focus")
	}
}

func TestAuditModelCannotChooseBetweenAlternativeAmounts(t *testing.T) {
	for _, expression := range []string{"", "300"} {
		t.Run(expression, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			args := `{"amount":"300","spent_at":"today","comment":"кофе"}`
			if expression != "" {
				args = `{"amount":"300","amount_text":"300","spent_at":"today","comment":"кофе"}`
			}
			handle(t, orchestrator, model, 1, 101, "кофе 200 или 300", "create_expense", args)
			if len(store.repo.items) != 0 {
				t.Fatal("model selected an explicitly ambiguous monetary value")
			}
			handle(t, orchestrator, model, 2, 101, "300", "answer_clarification", `{"value":"300"}`)
			if len(store.repo.items) != 1 || store.repo.items[1].Amount.String() != "300.00" {
				t.Fatal("explicit amount clarification did not recover")
			}
		})
	}
}

func TestAuditStatedPreviousAmountCannotBeOmittedOrSwapped(t *testing.T) {
	for _, test := range []struct{ amount, args string }{
		{"300", `{"amount":"450"}`},
		{"450", `{"amount":"350","previous_amount":"450"}`},
	} {
		t.Run(test.args, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			handle(t, orchestrator, model, 1, 101, "такси "+test.amount, "create_expense", `{"amount":"`+test.amount+`","spent_at":"today","comment":"такси"}`)
			handle(t, orchestrator, model, 2, 101, "последнее было не 350, а 450", "update_expense", test.args)
			if store.repo.items[1].Amount.String() != test.amount+".00" {
				t.Fatal("model bypassed the source previous/new amount constraint")
			}
		})
	}
}

func TestAuditQuantityCannotReplaceExplicitPrice(t *testing.T) {
	for _, args := range []string{
		`{"amount":"2","spent_at":"today","comment":"кофе"}`,
		`{"amount":"2","amount_text":"2","spent_at":"today","comment":"кофе"}`,
	} {
		t.Run(args, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			handle(t, orchestrator, model, 1, 101, "купил 2 кофе за 500", "create_expense", args)
			if len(store.repo.items) != 0 {
				t.Fatal("quantity replaced the explicitly marked price")
			}
		})
	}
}

func TestAuditReadRequestCannotSeedPendingMutation(t *testing.T) {
	orchestrator, model, _ := setup(t)
	handle(t, orchestrator, model, 1, 101, "мои расходы", "clarify", `{"field":"category","intent":"update_expense","id":"1"}`)
	if orchestrator.states[101].pending != nil {
		t.Fatal("read request seeded a deferred mutation")
	}
}
