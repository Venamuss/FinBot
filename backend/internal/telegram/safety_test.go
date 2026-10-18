package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/internal/httpapi"
	"github.com/Venamuss/FinBot/internal/postgres"
)

func assertAuditExpenses(t *testing.T, workflow *modificationWorkflow, amounts ...string) {
	t.Helper()
	page, err := postgres.New(workflow.pool).List(context.Background(), expense.Filter{UserID: 101, Sort: "id", Order: "asc", Limit: 100})
	if err != nil || len(page.Items) != len(amounts) {
		t.Fatalf("unexpected expense count: %d; error: %v", len(page.Items), err)
	}
	for index, amount := range amounts {
		if page.Items[index].Amount.String() != amount || page.Items[index].UserID != 101 {
			t.Fatalf("unexpected expense at position %d", index)
		}
	}
}

func TestAuditFullCRUDSequenceIntegration(t *testing.T) {
	workflow := newModificationWorkflow(t)
	workflow.send(101, "тестовое такси 300", "create_expense", `{"amount":"300","amount_text":"300","spent_at":"today","category":"транспорт","comment":"тестовое такси"}`)
	assertAuditExpenses(t, workflow, "300.00")
	original := workflow.item(1)
	workflow.send(101, "последнее было 450", "update_expense", `{"amount":"450"}`)
	assertAuditExpenses(t, workflow, "450.00")
	beforeRead := expenseSnapshot(t, workflow)
	workflow.send(101, "мои расходы", "list_expenses", `{}`)
	if expenseSnapshot(t, workflow) != beforeRead {
		t.Fatal("listing mutated expenses")
	}
	workflow.send(101, "измени сумму тестового такси на 600", "update_expense", `{"query":"тестовое такси","amount":"600","request_context":"new"}`)
	assertAuditExpenses(t, workflow, "600.00")
	updated := workflow.item(1)
	original.Amount = updated.Amount
	original.UpdatedAt = updated.UpdatedAt
	if !reflect.DeepEqual(original, updated) {
		t.Fatal("amount correction changed unrelated fields")
	}
	reply := workflow.send(101, "удали тестовое такси", "delete_expense", `{"query":"тестовое такси","request_context":"new"}`)
	if !strings.Contains(reply, "да или нет") || !strings.Contains(reply, "#1") || !strings.Contains(reply, "600 ₽") {
		t.Fatal("confirmation did not identify the resolved version")
	}
	assertAuditExpenses(t, workflow, "600.00")
	calls := workflow.calls.Load()
	workflow.send(101, "да", "delete_expense", `{}`)
	assertAuditExpenses(t, workflow)
	if workflow.calls.Load() != calls {
		t.Fatal("delete confirmation called the model")
	}
	workflow.send(101, "мои расходы", "list_expenses", `{}`)
	assertAuditExpenses(t, workflow)
	workflow.send(101, "да", "create_expense", `{"amount":"600","spent_at":"today"}`)
	assertAuditExpenses(t, workflow)
}

func TestAuditAmbiguousDeletionAndMixedStateIntegration(t *testing.T) {
	workflow := newModificationWorkflow(t)
	for range 2 {
		workflow.send(101, "такси 300", "create_expense", `{"amount":"300","spent_at":"today","comment":"такси"}`)
	}
	before := expenseSnapshot(t, workflow)
	reply := workflow.send(101, "удали такси", "delete_expense", `{"query":"такси"}`)
	if !strings.Contains(reply, "2.") {
		t.Fatal("ambiguous deletion did not request a choice")
	}
	workflow.send(101, "да", "choose_expense", `{"choice":"1"}`)
	if expenseSnapshot(t, workflow) != before {
		t.Fatal("yes bypassed ambiguous selection")
	}
	reply = workflow.send(101, "2", "choose_expense", `{"choice":"2"}`)
	if !strings.Contains(reply, "да или нет") || !strings.Contains(reply, "#1") || expenseSnapshot(t, workflow) != before {
		t.Fatal("selection skipped confirmation or chose the wrong target")
	}
	workflow.send(101, "нет", "delete_expense", `{}`)
	if expenseSnapshot(t, workflow) != before {
		t.Fatal("declining confirmation changed expenses")
	}
	workflow.send(101, "кофе", "clarify", `{"field":"amount","intent":"create_expense","comment":"кофе"}`)
	workflow.send(101, "еда 50", "create_expense", `{"amount":"50","spent_at":"today","comment":"еда"}`)
	assertAuditExpenses(t, workflow, "300.00", "300.00", "50.00")
	workflow.send(101, "мои расходы", "list_expenses", `{"limit":"1"}`)
	workflow.send(101, "ещё", "navigate_expenses", `{"direction":"next"}`)
	workflow.send(101, "измени такси на 700", "update_expense", `{"query":"такси","amount":"700","request_context":"new"}`)
	workflow.send(101, "стоп", "delete_expense", `{}`)
	workflow.send(101, "чай 120", "create_expense", `{"amount":"120","spent_at":"today","comment":"чай"}`)
	workflow.send(101, "мои расходы", "list_expenses", `{}`)
	assertAuditExpenses(t, workflow, "300.00", "300.00", "50.00", "120.00")
}

func TestAuditPendingDeletePanelChangesIntegration(t *testing.T) {
	for _, change := range []struct{ name, method, body string }{
		{"amount", http.MethodPatch, `{"amount":"400"}`},
		{"comment", http.MethodPatch, `{"comment":"новое описание"}`},
		{"owner", http.MethodPatch, `{"user_id":"202"}`},
		{"deleted", http.MethodDelete, ""},
	} {
		t.Run(change.name, func(t *testing.T) {
			workflow := newModificationWorkflow(t)
			for range 2 {
				workflow.send(101, "такси 300", "create_expense", `{"amount":"300","spent_at":"today","comment":"такси"}`)
			}
			workflow.send(101, "удали расход #1", "delete_expense", `{"id":"1","request_context":"new"}`)
			admin := httpapi.New(expense.NewService(postgres.New(workflow.pool)), time.Second)
			request := httptest.NewRequest(change.method, "/api/expenses/1", bytes.NewBufferString(change.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			admin.ServeHTTP(response, request)
			if response.Code != http.StatusOK && response.Code != http.StatusNoContent {
				t.Fatalf("panel write failed: %d", response.Code)
			}
			before := expenseSnapshot(t, workflow)
			reply := workflow.send(101, "да", "delete_expense", `{}`)
			if !strings.Contains(reply, "не найдена или была изменена") || expenseSnapshot(t, workflow) != before {
				t.Fatal("stale confirmation mutated an expense")
			}
			workflow.send(101, "да", "delete_expense", `{}`)
			if expenseSnapshot(t, workflow) != before {
				t.Fatal("repeated confirmation switched to another expense")
			}
		})
	}
}

func TestAuditDeleteConfirmationIsolationIntegration(t *testing.T) {
	workflow := newModificationWorkflow(t)
	for _, owner := range []int64{101, 202} {
		workflow.send(owner, "такси 300", "create_expense", `{"amount":"300","spent_at":"today","comment":"такси"}`)
	}
	workflow.send(101, "удали такси", "delete_expense", `{"query":"такси"}`)
	before := expenseSnapshot(t, workflow)
	workflow.send(202, "да", "delete_expense", `{}`)
	workflow.send(303, "да", "delete_expense", `{}`)
	workflow.send(202, "измени расход #1 на 900", "update_expense", `{"id":"1","amount":"900"}`)
	workflow.send(202, "удали расход #1", "delete_expense", `{"id":"1"}`)
	if expenseSnapshot(t, workflow) != before {
		t.Fatal("another user changed A's expense or confirmation")
	}
	workflow.send(101, "да", "delete_expense", `{}`)
	if workflow.count(101) != 0 || workflow.count(202) != 1 {
		t.Fatal("confirmation was not scoped to A")
	}
}

func TestAuditModelFailureRecoveryIntegration(t *testing.T) {
	for _, failure := range []string{"timeout", "429", "invalid_arguments"} {
		t.Run(failure, func(t *testing.T) {
			workflow := newModificationWorkflow(t)
			workflow.send(101, "такси 300", "create_expense", `{"amount":"300","spent_at":"today","comment":"такси"}`)
			workflow.send(101, "кофе", "clarify", `{"field":"amount","intent":"create_expense","comment":"кофе"}`)
			before := expenseSnapshot(t, workflow)
			switch failure {
			case "timeout":
				workflow.modelTimeout.Store(true)
			case "429":
				workflow.modelStatus.Store(http.StatusTooManyRequests)
			}
			workflow.send(101, "измени сумму на 900", "update_expense", `{"amount":900}`)
			workflow.modelTimeout.Store(false)
			workflow.modelStatus.Store(0)
			if expenseSnapshot(t, workflow) != before {
				t.Fatal("model failure changed expenses")
			}
			calls := workflow.calls.Load()
			if err := workflow.bot.Process(context.Background(), workflow.update); err != nil {
				t.Fatal(err)
			}
			<-workflow.replies
			if workflow.calls.Load() != calls || expenseSnapshot(t, workflow) != before {
				t.Fatal("duplicate failed update reran model or mutation")
			}
			workflow.send(101, "120", "answer_clarification", `{"value":"120"}`)
			assertAuditExpenses(t, workflow, "300.00", "120.00")
		})
	}
}

func TestAuditDeleteCommitAndDeliveryRecoveryIntegration(t *testing.T) {
	workflow := newModificationWorkflow(t)
	for range 2 {
		workflow.send(101, "такси 300", "create_expense", `{"amount":"300","spent_at":"today","comment":"такси"}`)
	}
	workflow.send(101, "удали расход #1", "delete_expense", `{"id":"1","request_context":"new"}`)
	_, err := workflow.pool.Exec(context.Background(), `CREATE FUNCTION reject_audit_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic audit commit failure'; END $$;
CREATE CONSTRAINT TRIGGER reject_audit_commit AFTER INSERT ON telegram_updates DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_audit_commit()`)
	if err != nil {
		t.Fatal(err)
	}
	before := expenseSnapshot(t, workflow)
	reply := workflow.process(101, "да", true)
	if strings.Contains(reply, "Расход удалён") || expenseSnapshot(t, workflow) != before {
		t.Fatal("failed commit changed data or reported success")
	}
	if _, err := workflow.pool.Exec(context.Background(), `DROP TRIGGER reject_audit_commit ON telegram_updates`); err != nil {
		t.Fatal(err)
	}
	// Retry the same uncommitted confirmation. The write commits but delivery
	// fails on all three transport attempts. A replay must only resend its result.
	workflow.deliveryFailures.Store(3)
	if err := workflow.bot.Process(context.Background(), workflow.update); err == nil {
		t.Fatal("Telegram delivery failure was ignored")
	}
	assertAuditExpenses(t, workflow, "300.00")
	if workflow.item(2).ID != 2 {
		t.Fatal("wrong expense survived")
	}
	mutations, calls := workflow.mutations.Load(), workflow.calls.Load()
	if err := workflow.bot.Process(context.Background(), workflow.update); err != nil {
		t.Fatal(err)
	}
	reply = <-workflow.replies
	if !strings.Contains(reply, "Расход удалён") || workflow.mutations.Load() != mutations || workflow.calls.Load() != calls {
		t.Fatal("delivery retry did not replay committed deletion")
	}
	workflow.send(101, "да", "delete_expense", `{}`)
	assertAuditExpenses(t, workflow, "300.00")
}

func TestAuditWrongToolsCannotMutateIntegration(t *testing.T) {
	workflow := newModificationWorkflow(t)
	workflow.send(101, "такси 300", "create_expense", `{"amount":"300","spent_at":"today","comment":"такси"}`)
	before := expenseSnapshot(t, workflow)
	for _, name := range []string{"create_expense", "update_expense", "delete_expense", "choose_expense"} {
		args := map[string]string{"amount": "900"}
		switch name {
		case "create_expense":
			args["spent_at"] = "today"
		case "delete_expense":
			args = map[string]string{}
		case "choose_expense":
			args = map[string]string{"choice": "1"}
		}
		encoded, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		workflow.send(101, "мои расходы", name, string(encoded))
		if expenseSnapshot(t, workflow) != before {
			t.Fatal("read request executed a wrong model tool")
		}
	}
}
