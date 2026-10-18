package conversation

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestExplicitCancellationMatchesWholeCommand(t *testing.T) {
	for _, text := range []string{"стоп", "отмена", "отмени", "cancel", "  СТОП!  ", "Cancel.", "отменить", "не надо", "отмени действие"} {
		if !explicitCancellation(text) {
			t.Errorf("standalone command was not recognized: %q", text)
		}
	}
	for _, text := range []string{"", "купил стоп кран 500", "стоп кран 500", "cancel subscription 120", "отмена подписки 350", "отмени такси", "стоп123", "не отменяй"} {
		if explicitCancellation(text) {
			t.Errorf("expense text was mistaken for cancellation: %q", text)
		}
	}
}

func TestCancellationBypassesAnyModelResult(t *testing.T) {
	for _, test := range []struct{ text, name, args string }{
		{"стоп", "delete_expense", `{}`},
		{"отмена", "update_expense", `{"amount":"999"}`},
		{"отмени", "create_expense", `{"amount":"999","spent_at":"today"}`},
		{"cancel", "choose_expense", `{"choice":"1"}`},
	} {
		t.Run(test.text, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			handle(t, orchestrator, model, 1, 101, "кофе 350", "create_expense", `{"amount":"350","spent_at":"today","comment":"кофе"}`)
			before := store.repo.items[1]
			calls := model.calls
			reply := handle(t, orchestrator, model, 2, 101, test.text, test.name, test.args)
			if reply != "Нет активной операции для отмены." || model.calls != calls || len(store.repo.items) != 1 || !reflect.DeepEqual(before, store.repo.items[1]) {
				t.Fatal("model output overrode cancellation")
			}
			model.err = errors.New("synthetic unavailable model")
			reply, err := orchestrator.Handle(context.Background(), 3, 101, test.text, time.Now())
			if err != nil || reply != "Нет активной операции для отмены." || model.calls != calls {
				t.Fatal("cancellation depended on model availability")
			}
		})
	}
}

func TestFailedCancellationPreservesPendingState(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "кофе", "clarify", `{"field":"amount","intent":"create_expense","comment":"кофе"}`)
	before := orchestrator.states[101].pending
	calls := model.calls
	store.fail = true
	reply, err := orchestrator.Handle(context.Background(), 2, 101, "стоп", time.Now())
	if err == nil || reply != "" || orchestrator.states[101].pending != before || model.calls != calls {
		t.Fatal("uncommitted cancellation changed state or confirmed success")
	}
	store.fail = false
	reply, err = orchestrator.Handle(context.Background(), 2, 101, "стоп", time.Now())
	if err != nil || reply != "Выбор отменён." || orchestrator.states[101].pending != nil || len(store.repo.items) != 0 || model.calls != calls {
		t.Fatal("cancellation did not recover after failed transaction")
	}
}

func TestCancellationAfterStateExpiryAndEmptyAllowlist(t *testing.T) {
	orchestrator, model, store := setup(t)
	now := time.Now()
	orchestrator.now = func() time.Time {
		return now
	}
	handle(t, orchestrator, model, 1, 101, "кофе", "clarify", `{"field":"amount","comment":"кофе"}`)
	now = now.Add(30 * time.Minute)
	calls := model.calls
	reply := handle(t, orchestrator, model, 2, 101, "стоп", "delete_expense", `{}`)
	if reply != "Нет активной операции для отмены." || model.calls != calls || len(store.repo.items) != 0 || orchestrator.states[101].pending != nil {
		t.Fatal("cancellation restored expired pending state")
	}
	orchestrator.allowed = nil
	reads := store.reads
	reply = handle(t, orchestrator, model, 3, 101, "стоп", "delete_expense", `{}`)
	if reply != Denied || model.calls != calls || store.reads != reads {
		t.Fatal("cancellation bypassed the empty allowlist")
	}
}
