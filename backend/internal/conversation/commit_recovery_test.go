package conversation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type acknowledgementLossStore struct {
	Store
	updateID int64
}

func (store *acknowledgementLossStore) Execute(ctx context.Context, updateID, owner int64, execute func(context.Context, Repositories) (string, error)) (string, bool, error) {
	reply, committed, err := store.Store.Execute(ctx, updateID, owner, execute)
	if err == nil && committed && updateID == store.updateID {
		store.updateID = 0
		return "", false, fmt.Errorf("synthetic acknowledgement loss: %w", ErrCommitUncertain)
	}
	return reply, committed, err
}

func TestCancellationCommitRecovery(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(fmt.Sprintf("uncertain=%t", uncertain), func(t *testing.T) {
			orchestrator, model, store := setup(t)
			if uncertain {
				orchestrator.store = &acknowledgementLossStore{Store: store, updateID: 5}
			}

			handle(t, orchestrator, model, 1, 101, "кофе 350", "create_expense", `{"amount":"350","spent_at":"today","comment":"кофе"}`)
			handle(t, orchestrator, model, 2, 202, "такси 450", "create_expense", `{"amount":"450","spent_at":"today","comment":"такси"}`)
			handle(t, orchestrator, model, 3, 202, "измени сумму такси", "clarify", `{"field":"amount","intent":"update_expense","query":"такси"}`)
			otherState := orchestrator.states[202]
			handle(t, orchestrator, model, 4, 101, "удали последнюю трату", "delete_expense", `{}`)
			if orchestrator.states[101].pending.field != "delete_confirmation" {
				t.Fatal("deletion was not awaiting confirmation")
			}

			calls := model.calls
			reply, err := orchestrator.Handle(context.Background(), 5, 101, "стоп", time.Now())
			if uncertain {
				if !errors.Is(err, ErrCommitUncertain) || reply != "" {
					t.Fatal("uncertain commit claimed success")
				}
				if _, exists := orchestrator.states[101]; exists {
					t.Fatal("uncertain owner state was retained")
				}
			} else if err != nil || reply != "Выбор отменён." {
				t.Fatal("normal cancellation failed")
			}
			if orchestrator.states[202].pending != otherState.pending || !orchestrator.states[202].touched.Equal(otherState.touched) {
				t.Fatal("another owner's state was invalidated")
			}

			reply, err = orchestrator.Handle(context.Background(), 5, 101, "стоп", time.Now())
			if err != nil || reply != "Выбор отменён." || model.calls != calls {
				t.Fatal("durable cancellation did not replay without planning")
			}
			handle(t, orchestrator, model, 6, 101, "да", "delete_expense", `{}`)
			if len(store.repo.items) != 2 || orchestrator.states[101].pending != nil || model.calls != calls {
				t.Fatal("confirmation revived canceled deletion")
			}

			handle(t, orchestrator, model, 7, 101, "чай", "clarify", `{"field":"amount","intent":"create_expense","comment":"чай"}`)
			newer := orchestrator.states[101].pending
			reply, err = orchestrator.Handle(context.Background(), 5, 101, "стоп", time.Now())
			if err != nil || reply != "Выбор отменён." || orchestrator.states[101].pending != newer || len(store.repo.items) != 2 {
				t.Fatal("old replay changed newer state or expenses")
			}
			handle(t, orchestrator, model, 8, 101, "120", "answer_clarification", `{"value":"120"}`)
			handle(t, orchestrator, model, 9, 202, "500", "answer_clarification", `{"value":"500"}`)
			if len(store.repo.items) != 3 || store.repo.items[2].Amount.String() != "500.00" || store.repo.items[3].Comment == nil || *store.repo.items[3].Comment != "чай" {
				t.Fatal("valid clarification was lost during recovery or replay")
			}
		})
	}
}

func TestUncertainSavedFailureInvalidatesPendingState(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "кофе 350", "create_expense", `{"amount":"350","spent_at":"today"}`)
	handle(t, orchestrator, model, 2, 101, "удали последнюю трату", "delete_expense", `{}`)
	orchestrator.store = &acknowledgementLossStore{Store: store, updateID: 3}
	model.err = errors.New("synthetic model unavailable")

	reply, err := orchestrator.Handle(context.Background(), 3, 101, "новый запрос", time.Now())
	if !errors.Is(err, ErrCommitUncertain) || reply != "" || orchestrator.states[101].pending != nil {
		t.Fatal("saved failure retained uncertain pending state")
	}
	model.err = nil
	reply, err = orchestrator.Handle(context.Background(), 3, 101, "новый запрос", time.Now())
	if err != nil || !strings.Contains(reply, "Попробуйте позже") {
		t.Fatal("saved failure did not replay")
	}
	handle(t, orchestrator, model, 4, 101, "да", "delete_expense", `{}`)
	if len(store.repo.items) != 1 {
		t.Fatal("uncertain reply-only transaction left deletion armed")
	}
}
