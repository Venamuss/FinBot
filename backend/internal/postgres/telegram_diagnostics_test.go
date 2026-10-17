package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/conversation"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRetryTransactionClassification(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		retry bool
	}{
		{name: "success"},
		{name: "serialization", err: &pgconn.PgError{Code: "40001"}, retry: true},
		{name: "deadlock", err: &pgconn.PgError{Code: "40P01"}, retry: true},
		{name: "wrapped serialization", err: fmt.Errorf("callback: %w", &pgconn.PgError{Code: "40001"}), retry: true},
		{name: "wrapped deadlock", err: &transactionError{stage: "commit", cause: &pgconn.PgError{Code: "40P01"}}, retry: true},
		{name: "constraint", err: &pgconn.PgError{Code: "23514"}},
		{name: "connection loss", err: &pgconn.PgError{Code: "08006"}},
		{name: "network", err: &net.OpError{Op: "read", Net: "tcp", Err: errors.New("private-network")}},
		{name: "canceled", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "message is not a SQLSTATE", err: errors.New("40001 40P01")},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := retryTransaction(test.err); got != test.retry {
				t.Fatalf("retry=%t, want %t", got, test.retry)
			}
		})
	}
}

func TestTransactionErrorPreservesCause(t *testing.T) {
	databaseError := &pgconn.PgError{Code: "40001", Message: "private-message", Detail: "private-expense"}
	failure := &transactionError{stage: "execute", cause: fmt.Errorf("private-context: %w", databaseError)}
	var actual *pgconn.PgError
	if !errors.As(failure, &actual) || actual != databaseError || !errors.Is(failure, databaseError) {
		t.Fatal("database error identity was lost")
	}
	if failure.Error() != "Telegram transaction failed at execute" || strings.Contains(failure.Error(), "private-") {
		t.Fatal("transaction error exposed the cause")
	}
	canceled := &transactionError{stage: "begin", cause: context.Canceled}
	if !errors.Is(canceled, context.Canceled) {
		t.Fatal("cancellation identity was lost")
	}
}

func TestTelegramRetryDiagnosticsIntegration(t *testing.T) {
	cases := []struct {
		name      string
		code      string
		failures  int
		cancel    bool
		attempts  int
		committed bool
		category  string
	}{
		{name: "serialization recovers", code: "40001", failures: 2, attempts: 3, committed: true, category: "serialization_failure"},
		{name: "deadlock recovers", code: "40P01", failures: 1, attempts: 2, committed: true, category: "deadlock"},
		{name: "serialization limit", code: "40001", failures: 3, attempts: 3, category: "serialization_failure"},
		{name: "deadlock limit", code: "40P01", failures: 3, attempts: 3, category: "deadlock"},
		{name: "constraint is not retried", code: "23514", failures: 1, attempts: 1, category: "database"},
		{name: "cancel stops serialization retry", code: "40001", failures: 3, cancel: true, attempts: 1, category: "serialization_failure"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			pool := testPool(t)
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
			t.Cleanup(func() {
				slog.SetDefault(previous)
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cause := &pgconn.PgError{Code: test.code, Message: "private-database-message", Detail: "private-expense"}
			attempts := 0
			reply, committed, err := NewTelegramStore(pool).Execute(ctx, 42, 101, func(ctx context.Context, repo conversation.Repositories) (string, error) {
				attempts++
				if _, err := createInTransaction(ctx, repo, "101"); err != nil {
					return "", err
				}
				if attempts <= test.failures {
					if test.cancel {
						cancel()
					}
					return "", fmt.Errorf("private-callback: %w", cause)
				}
				return "private-reply", nil
			})
			if attempts != test.attempts || committed != test.committed {
				t.Fatalf("attempts=%d committed=%t", attempts, committed)
			}
			if test.committed {
				if err != nil || reply != "private-reply" {
					t.Fatal("retry did not return the committed result")
				}
				assertCounts(t, pool, 1, 1)
				_, replayCommitted, replayErr := NewTelegramStore(pool).Execute(context.Background(), 42, 101, func(context.Context, conversation.Repositories) (string, error) {
					t.Fatal("replay executed the callback")
					return "", nil
				})
				if replayErr != nil || replayCommitted {
					t.Fatal("replay was reported as a new commit")
				}
			} else {
				if !errors.Is(err, cause) || reply != "" {
					t.Fatal("failed transaction lost its cause or exposed a partial result")
				}
				assertCounts(t, pool, 0, 0)
			}
			if strings.Contains(output.String(), "private-") {
				t.Fatal("transaction diagnostics leaked private data")
			}
			decoder := json.NewDecoder(&output)
			for attempt := 1; attempt <= test.failures && attempt <= test.attempts; attempt++ {
				var record map[string]any
				if err := decoder.Decode(&record); err != nil {
					t.Fatal(err)
				}
				if record["stage"] != "execute" || record["error_category"] != test.category || record["attempt"] != float64(attempt) || record["update_id"] != float64(42) {
					t.Fatalf("incorrect safe transaction metadata: %v", record)
				}
				if !test.committed && attempt == test.attempts {
					if record["msg"] != "Telegram transaction failed" || record["level"] != "DEBUG" || record["duration_ms"] == nil {
						t.Fatal("missing terminal diagnostic")
					}
				} else if record["msg"] != "Telegram transaction retry scheduled" || record["level"] != "WARN" {
					t.Fatal("missing retry diagnostic")
				}
			}
			if decoder.More() {
				t.Fatal("unexpected duplicate transaction diagnostics")
			}
		})
	}
}

func TestTelegramTransactionStagesIntegration(t *testing.T) {
	for _, stage := range []string{"begin", "reserve_update", "lookup_response", "execute", "save_response", "commit"} {
		t.Run(stage, func(t *testing.T) {
			pool := testPool(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			owner := int64(101)
			switch stage {
			case "begin":
				cancel()
			case "reserve_update":
				owner = 0
			case "lookup_response":
				_, err := pool.Exec(ctx, `INSERT INTO telegram_updates (update_id, user_id, response) VALUES (42, 101, '')`)
				if err != nil {
					t.Fatal(err)
				}
			case "save_response":
				_, err := pool.Exec(ctx, `ALTER TABLE telegram_updates ADD CHECK (response <> 'private-reply')`)
				if err != nil {
					t.Fatal(err)
				}
			case "commit":
				// A deferred foreign key fails only at commit, after saving the reply.
				for _, query := range []string{
					`CREATE TABLE result_guard (response TEXT PRIMARY KEY)`,
					`ALTER TABLE telegram_updates ADD FOREIGN KEY (response) REFERENCES result_guard(response) DEFERRABLE INITIALLY DEFERRED`,
				} {
					if _, err := pool.Exec(ctx, query); err != nil {
						t.Fatal(err)
					}
				}
			}
			calls := 0
			_, committed, err := NewTelegramStore(pool).Execute(ctx, 42, owner, func(context.Context, conversation.Repositories) (string, error) {
				calls++
				if stage == "execute" {
					return "", nil
				}
				return "private-reply", nil
			})
			var failure *transactionError
			if committed || !errors.As(err, &failure) || failure.stage != stage || strings.Contains(err.Error(), "private-") {
				t.Fatal("transaction error lost its safe stage")
			}
			if stage == "begin" && !errors.Is(err, context.Canceled) {
				t.Fatal("begin error lost cancellation identity")
			}
			if stage == "begin" || stage == "reserve_update" || stage == "lookup_response" {
				if calls != 0 {
					t.Fatal("failed transaction reached the callback")
				}
			} else if calls != 1 {
				t.Fatal("nonretryable failure repeated the callback")
			}
			updates := 0
			if stage == "lookup_response" {
				updates = 1
			}
			assertCounts(t, pool, 0, updates)
		})
	}
}
