package telegram

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/conversation"
	"github.com/Venamuss/FinBot/internal/expense"
)

// Count calls before SQL executes: an unchanged snapshot alone would miss a
// mistakenly invoked mutation that matched no rows or wrote identical values.
type mutationCountingStore struct {
	conversation.Store
	calls *atomic.Int32
}

func (store *mutationCountingStore) Execute(ctx context.Context, updateID, owner int64, execute func(context.Context, conversation.Repositories) (string, error)) (string, bool, error) {
	return store.Store.Execute(ctx, updateID, owner, func(ctx context.Context, repo conversation.Repositories) (string, error) {
		return execute(ctx, &mutationCountingRepository{Repositories: repo, calls: store.calls})
	})
}

type mutationCountingRepository struct {
	conversation.Repositories
	calls *atomic.Int32
}

func (repo *mutationCountingRepository) Create(ctx context.Context, item expense.Expense) (expense.Expense, error) {
	repo.calls.Add(1)
	return repo.Repositories.Create(ctx, item)
}

func (repo *mutationCountingRepository) UpdateOwned(ctx context.Context, owner, id int64, version time.Time, changes expense.Changes) (expense.Expense, error) {
	repo.calls.Add(1)
	return repo.Repositories.UpdateOwned(ctx, owner, id, version, changes)
}

func (repo *mutationCountingRepository) DeleteOwned(ctx context.Context, owner, id int64, version time.Time) error {
	repo.calls.Add(1)
	return repo.Repositories.DeleteOwned(ctx, owner, id, version)
}

func expenseSnapshot(t *testing.T, workflow *modificationWorkflow) string {
	t.Helper()
	var snapshot string
	err := workflow.pool.QueryRow(context.Background(), `SELECT COALESCE(jsonb_agg(to_jsonb(e) ORDER BY id), '[]'::jsonb)::text FROM expenses e`).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func cancelWithoutMutation(t *testing.T, workflow *modificationWorkflow, owner int64, text, wantReply, tool, args string) {
	t.Helper()
	before := expenseSnapshot(t, workflow)
	modelCalls, mutationCalls := workflow.calls.Load(), workflow.mutations.Load()
	if reply := workflow.send(owner, text, tool, args); reply != wantReply {
		t.Errorf("unexpected cancellation reply: %q", reply)
	}
	if workflow.calls.Load() != modelCalls {
		t.Error("cancellation reached the LLM")
	}
	if workflow.mutations.Load() != mutationCalls {
		t.Error("cancellation invoked an expense mutation")
	}
	if expenseSnapshot(t, workflow) != before {
		t.Error("cancellation changed stored expenses")
	}
}

func TestCancellationIdleAndAfterDeletionIntegration(t *testing.T) {
	for _, afterDeletion := range []bool{false, true} {
		name := "idle"
		if afterDeletion {
			name = "after_deletion"
		}
		t.Run(name, func(t *testing.T) {
			workflow := newModificationWorkflow(t)
			for range 4 {
				workflow.send(101, "такси 350", "create_expense", `{"amount":"350","spent_at":"today","comment":"такси"}`)
			}
			if workflow.count(101) != 4 {
				t.Fatal("synthetic expenses were not created")
			}
			if afterDeletion {
				reply := workflow.send(101, "удали последнюю трату", "delete_expense", `{}`)
				if !strings.Contains(reply, "Расход удалён") || workflow.count(101) != 3 {
					t.Fatal("explicit deletion failed")
				}
			}
			// Deliberately hostile output reproduces the original deletion chain.
			// Cancellation must bypass it entirely, including with a live focus.
			for range 3 {
				cancelWithoutMutation(t, workflow, 101, "стоп", "Нет активной операции для отмены.", "delete_expense", `{}`)
			}
		})
	}
}

func TestCancellationPendingWorkflowsIntegration(t *testing.T) {
	tests := []struct{ name, text, tool, args string }{
		{"delete_selection", "удали такси", "delete_expense", `{"query":"такси"}`},
		{"delete_target", "удали трату", "clarify", `{"field":"target","intent":"delete_expense"}`},
		{"update_selection", "измени такси на 700", "update_expense", `{"query":"такси","amount":"700"}`},
		{"update_confirmation", "последнее было не 100, а 700", "update_expense", `{"previous_amount":"100","amount":"700"}`},
		{"update_amount", "измени сумму такси", "clarify", `{"field":"amount","intent":"update_expense","query":"такси"}`},
		{"update_category", "измени категорию такси", "clarify", `{"field":"category","intent":"update_expense","query":"такси"}`},
		{"update_date", "измени дату такси", "clarify", `{"field":"spent_at","intent":"update_expense","query":"такси"}`},
		{"update_target", "измени сумму на 700", "clarify", `{"field":"target","intent":"update_expense","amount":"700"}`},
		{"create_amount", "кофе", "clarify", `{"field":"amount","intent":"create_expense","comment":"кофе"}`},
		{"create_currency", "кофе 10$", "create_expense", `{"amount":"10","spent_at":"today","comment":"кофе"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workflow := newModificationWorkflow(t)
			for range 2 {
				workflow.send(101, "такси 350", "create_expense", `{"amount":"350","spent_at":"today","comment":"такси"}`)
			}
			before := expenseSnapshot(t, workflow)
			mutations := workflow.mutations.Load()
			workflow.send(101, test.text, test.tool, test.args)
			if expenseSnapshot(t, workflow) != before || workflow.mutations.Load() != mutations {
				t.Fatal("pending workflow mutated expenses before clarification")
			}
			commands := []string{"стоп", "отмена", "отмени", "cancel"}
			if strings.HasPrefix(test.name, "update_") {
				commands[0], commands[1] = commands[1], commands[0]
			}
			for index, command := range commands {
				want := "Нет активной операции для отмены."
				if index == 0 {
					want = "Выбор отменён."
				}
				cancelWithoutMutation(t, workflow, 101, command, want, "choose_expense", `{"choice":"1"}`)
			}
			// A stale model-selected choice cannot resurrect the canceled operation.
			workflow.send(101, "первая", "choose_expense", `{"choice":"1"}`)
			if expenseSnapshot(t, workflow) != before || workflow.mutations.Load() != mutations {
				t.Fatal("canceled pending operation was executed later")
			}
			reply := workflow.send(101, "кофе 120", "create_expense", `{"amount":"120","spent_at":"today","comment":"кофе"}`)
			if !strings.Contains(reply, "Расход добавлен") || workflow.count(101) != 3 {
				t.Fatal("independent expense command after cancellation failed")
			}
		})
	}
}

func TestCancellationUserIsolationAndReplayIntegration(t *testing.T) {
	workflow := newModificationWorkflow(t)
	for _, owner := range []int64{101, 202} {
		for range 2 {
			workflow.send(owner, "такси 350", "create_expense", `{"amount":"350","spent_at":"today","comment":"такси"}`)
		}
		workflow.send(owner, "удали такси", "delete_expense", `{"query":"такси"}`)
	}
	cancelWithoutMutation(t, workflow, 101, "отмена", "Выбор отменён.", "delete_expense", `{"request_context":"new"}`)
	canceledUpdate := workflow.update
	workflow.send(101, "кофе", "clarify", `{"field":"amount","intent":"create_expense","comment":"кофе"}`)
	// Replaying an older cancellation must not clear the newer clarification.
	currentUpdate := workflow.update
	workflow.update = canceledUpdate
	calls, mutations := workflow.calls.Load(), workflow.mutations.Load()
	if err := workflow.bot.Process(context.Background(), canceledUpdate); err != nil {
		t.Fatal(err)
	}
	if reply := <-workflow.replies; reply != "Выбор отменён." || workflow.calls.Load() != calls || workflow.mutations.Load() != mutations {
		t.Fatal("duplicate cancellation did not replay its stored result")
	}
	workflow.update = currentUpdate
	cancelWithoutMutation(t, workflow, 303, "стоп", Denied, "delete_expense", `{}`)
	workflow.send(202, "первая", "choose_expense", `{"choice":"1"}`)
	if workflow.count(202) != 1 || workflow.count(101) != 2 {
		t.Fatal("cancellation affected another user's deletion workflow")
	}
	deletedUpdate := workflow.update
	calls, mutations = workflow.calls.Load(), workflow.mutations.Load()
	if err := workflow.bot.Process(context.Background(), deletedUpdate); err != nil {
		t.Fatal(err)
	}
	<-workflow.replies
	if workflow.calls.Load() != calls || workflow.mutations.Load() != mutations || workflow.count(202) != 1 {
		t.Fatal("duplicate deletion repeated the mutation")
	}
	workflow.send(101, "120", "answer_clarification", `{"value":"120"}`)
	if workflow.count(101) != 3 {
		t.Fatal("replayed cancellation cleared a newer clarification")
	}
	before := expenseSnapshot(t, workflow)
	mutations = workflow.mutations.Load()
	workflow.send(202, "первая", "choose_expense", `{"choice":"1"}`)
	if expenseSnapshot(t, workflow) != before || workflow.mutations.Load() != mutations {
		t.Fatal("completed deletion state was reused")
	}
}

func TestCancellationWordsInsideDescriptionsIntegration(t *testing.T) {
	workflow := newModificationWorkflow(t)
	for _, test := range []struct{ text, comment string }{
		{"стоп кран 120", "стоп кран"},
		{"cancel subscription 120", "cancel subscription"},
		{"отмена подписки 120", "отмена подписки"},
	} {
		calls, mutations := workflow.calls.Load(), workflow.mutations.Load()
		reply := workflow.send(101, test.text, "create_expense", `{"amount":"120","spent_at":"today","comment":"`+test.comment+`"}`)
		if !strings.Contains(reply, "Расход добавлен") || workflow.calls.Load() != calls+1 || workflow.mutations.Load() != mutations+1 {
			t.Fatal("description containing a cancellation word did not create an expense")
		}
	}
	if workflow.count(101) != 3 {
		t.Fatal("description expenses were not stored")
	}
}

func TestCancellationCommitFailureIntegration(t *testing.T) {
	workflow := newModificationWorkflow(t)
	for range 2 {
		workflow.send(101, "такси 350", "create_expense", `{"amount":"350","spent_at":"today","comment":"такси"}`)
	}
	workflow.send(101, "удали такси", "delete_expense", `{"query":"такси"}`)
	_, err := workflow.pool.Exec(context.Background(), `CREATE FUNCTION reject_cancellation_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic cancellation commit failure'; END $$;
 CREATE CONSTRAINT TRIGGER reject_cancellation_commit AFTER INSERT ON telegram_updates DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_cancellation_commit()`)
	if err != nil {
		t.Fatal(err)
	}
	before := expenseSnapshot(t, workflow)
	calls, mutations := workflow.calls.Load(), workflow.mutations.Load()
	reply := workflow.process(101, "стоп", true)
	if reply == "Выбор отменён." || workflow.calls.Load() != calls || workflow.mutations.Load() != mutations || expenseSnapshot(t, workflow) != before {
		t.Fatal("failed cancellation commit confirmed success, called the model or mutated expenses")
	}
	if _, err := workflow.pool.Exec(context.Background(), `DROP TRIGGER reject_cancellation_commit ON telegram_updates`); err != nil {
		t.Fatal(err)
	}
	// The cancellation callback ran, but its failed commit must leave the original
	// selection available. Only this explicit selection may now delete a record.
	reply = workflow.send(101, "первая", "choose_expense", `{"choice":"1"}`)
	if !strings.Contains(reply, "Расход удалён") || workflow.count(101) != 1 || workflow.mutations.Load() != mutations+1 {
		t.Fatal("failed cancellation commit discarded the pending deletion")
	}
}
