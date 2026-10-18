package postgres

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/conversation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type lostCommitAcknowledgementStore struct {
	conversation.Store
	updateID int64
}

func (store *lostCommitAcknowledgementStore) Execute(ctx context.Context, updateID, owner int64, execute func(context.Context, conversation.Repositories) (string, error)) (string, bool, error) {
	reply, committed, err := store.Store.Execute(ctx, updateID, owner, execute)
	if err == nil && committed && updateID == store.updateID {
		store.updateID = 0
		// PostgreSQL has actually committed. Report the same error category as a
		// lost COMMIT acknowledgement without requiring a timing-sensitive proxy.
		return "", false, &transactionError{stage: "commit", cause: io.ErrUnexpectedEOF}
	}
	return reply, committed, err
}

func TestCommitOutcomeClassification(t *testing.T) {
	for _, test := range []struct {
		name      string
		stage     string
		cause     error
		uncertain bool
	}{
		{name: "before commit", stage: "execute", cause: io.EOF},
		{name: "rollback tag", stage: "commit", cause: pgx.ErrTxCommitRollback},
		{name: "deferred constraint", stage: "commit", cause: &pgconn.PgError{Code: "23503"}},
		{name: "deferred trigger", stage: "commit", cause: &pgconn.PgError{Code: "P0001"}},
		{name: "serialization", stage: "commit", cause: &pgconn.PgError{Code: "40001"}},
		{name: "deadlock", stage: "commit", cause: &pgconn.PgError{Code: "40P01"}},
		{name: "lost acknowledgement", stage: "commit", cause: io.ErrUnexpectedEOF, uncertain: true},
		{name: "deadline", stage: "commit", cause: context.DeadlineExceeded, uncertain: true},
		{name: "unknown completion", stage: "commit", cause: &pgconn.PgError{Code: "40003"}, uncertain: true},
		{name: "connection exception", stage: "commit", cause: &pgconn.PgError{Code: "08007"}, uncertain: true},
		{name: "server termination", stage: "commit", cause: &pgconn.PgError{Code: "57P01", Severity: "FATAL"}, uncertain: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			failure := &transactionError{stage: test.stage, cause: test.cause}
			if errors.Is(failure, conversation.ErrCommitUncertain) != test.uncertain || !errors.Is(failure, test.cause) {
				t.Fatal("incorrect commit outcome or lost original cause")
			}
		})
	}
}

func TestCancellationUncertainCommitRecoveryIntegration(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := &lostCommitAcknowledgementStore{Store: NewTelegramStore(pool), updateID: 5}
	model := &integrationModel{pool: pool}
	orchestrator := conversation.New(model, store, map[int64]bool{101: true, 202: true}, time.UTC)
	send := func(updateID, owner int64, text, tool, args string) string {
		t.Helper()
		model.name, model.args = tool, args
		reply, err := orchestrator.Handle(ctx, updateID, owner, text, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return reply
	}

	send(1, 101, "кофе 350", "create_expense", `{"amount":"350","spent_at":"today","comment":"кофе"}`)
	send(2, 202, "такси 450", "create_expense", `{"amount":"450","spent_at":"today","comment":"такси"}`)
	send(3, 202, "измени сумму такси", "clarify", `{"field":"amount","intent":"update_expense","query":"такси"}`)
	send(4, 101, "удали последнюю трату", "delete_expense", `{}`)
	calls := model.calls
	reply, err := orchestrator.Handle(ctx, 5, 101, "стоп", time.Now())
	if !errors.Is(err, conversation.ErrCommitUncertain) || reply != "" {
		t.Fatal("acknowledgement loss did not report uncertainty")
	}
	saved, found, err := store.Lookup(ctx, 5, 101)
	if err != nil || !found || !strings.Contains(saved, "Выбор отменён.") {
		t.Fatal("cancellation did not durably commit before injected error")
	}
	if send(5, 101, "стоп", "delete_expense", `{}`) != "Выбор отменён." {
		t.Fatal("committed cancellation did not replay")
	}
	send(6, 101, "да", "delete_expense", `{}`)
	if model.calls != calls {
		t.Fatal("cancellation replay or idle confirmation reached the model")
	}
	assertCounts(t, pool, 2, 6)

	// A newer clarification survives both the old cancellation and old create.
	send(7, 101, "чай", "clarify", `{"field":"amount","intent":"create_expense","comment":"чай"}`)
	send(5, 101, "стоп", "delete_expense", `{}`)
	send(1, 101, "кофе 350", "create_expense", `{"amount":"350","spent_at":"today"}`)
	assertCounts(t, pool, 2, 7)
	send(8, 101, "120", "answer_clarification", `{"value":"120"}`)
	send(9, 202, "500", "answer_clarification", `{"value":"500"}`)
	assertCounts(t, pool, 3, 9)

	var unchanged, otherUpdated, newerCreated bool
	err = pool.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM expenses WHERE user_id=101 AND amount=350 AND comment='кофе'),
		EXISTS(SELECT 1 FROM expenses WHERE user_id=202 AND amount=500 AND comment='такси'),
		EXISTS(SELECT 1 FROM expenses WHERE user_id=101 AND amount=120 AND comment='чай')`).Scan(&unchanged, &otherUpdated, &newerCreated)
	if err != nil || !unchanged || !otherUpdated || !newerCreated {
		t.Fatal("recovery changed expenses or lost an owner's clarification")
	}
}
