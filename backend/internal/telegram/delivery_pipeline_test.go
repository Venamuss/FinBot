package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/botresponse"
	"github.com/Venamuss/FinBot/internal/conversation"
	"github.com/Venamuss/FinBot/internal/postgres"
)

func TestDeliveryPollingRecoveryIntegration(t *testing.T) {
	for _, test := range []struct {
		name              string
		denied            bool
		status            int
		apiCode           int
		failures          int
		rateLimit         bool
		processingFailure bool
		firstSends        int
	}{
		{name: "denied C forbidden then allowed A", denied: true, status: 403, failures: -1, firstSends: 1},
		{name: "denied C transient failure is best effort", denied: true, status: 503, failures: 3, firstSends: 3},
		{name: "committed forbidden then allowed B", status: 403, failures: -1, firstSends: 1},
		{name: "committed bad message then allowed B", status: 400, failures: -1, firstSends: 1},
		{name: "committed API forbidden then allowed B", status: 200, apiCode: 403, failures: -1, firstSends: 1},
		{name: "transient send exhausted then replay", status: 503, failures: 3, firstSends: 4},
		{name: "rate limit exhausted then replay", status: 429, apiCode: 429, failures: 3, rateLimit: true, firstSends: 4},
		{name: "uncommitted processing failure is retried", processingFailure: true, firstSends: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool := pipelinePool(t)
			output := captureBotLogs(t)
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()

			if test.processingFailure {
				// Sequence increments survive rollback, so only the first INSERT fails.
				_, err := pool.Exec(ctx, `CREATE SEQUENCE reject_first_expense;
					CREATE FUNCTION reject_first_expense() RETURNS trigger LANGUAGE plpgsql AS $$
					BEGIN
						IF nextval('reject_first_expense') = 1 THEN
							RAISE EXCEPTION 'synthetic expense failure';
						END IF;
						RETURN NEW;
					END $$;
					CREATE TRIGGER reject_first_expense BEFORE INSERT ON expenses
					FOR EACH ROW EXECUTE FUNCTION reject_first_expense()`)
				if err != nil {
					t.Fatal(err)
				}
			}

			firstOwner, secondOwner := int64(101), int64(202)
			if test.denied {
				firstOwner, secondOwner = 303, 101
			}
			updates := []Update{
				{ID: 40, Message: &Message{From: &User{ID: firstOwner}, Chat: Chat{ID: firstOwner, Type: "private"}, Date: 1792238400, Text: "private-message 350 create"}},
				{ID: 41, Message: &Message{From: &User{ID: secondOwner}, Chat: Chat{ID: secondOwner, Type: "private"}, Date: 1792238400, Text: "private-message 350 create"}},
			}

			var mutex sync.Mutex
			var firstAttempts []time.Time
			var secondSends int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mutex.Lock()
				defer mutex.Unlock()
				var request struct {
					ChatID int64  `json:"chat_id"`
					Text   string `json:"text"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if request.ChatID == firstOwner {
					firstAttempts = append(firstAttempts, time.Now())
				} else if request.ChatID == secondOwner {
					secondSends++
				} else {
					t.Error("unexpected delivery owner")
				}

				if request.Text == botresponse.ProcessingFailure {
					var expenses, results int
					err := pool.QueryRow(r.Context(), `SELECT (SELECT count(*) FROM expenses), (SELECT count(*) FROM telegram_updates)`).Scan(&expenses, &results)
					if err != nil || expenses != 0 || results != 0 {
						t.Error("failed processing persisted a mutation or result")
					}
					// Even terminal failure of this notice must not acknowledge the
					// underlying uncommitted operation.
					w.WriteHeader(http.StatusForbidden)
					return
				}

				if request.ChatID != 303 {
					var committed bool
					err := pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM telegram_updates WHERE user_id=$1 AND response=$2)`, request.ChatID, "finbot:html:v1\n"+request.Text).Scan(&committed)
					if err != nil || !committed {
						t.Error("delivery attempted before commit")
					}
				} else if request.Text != Denied {
					t.Error("denied owner received expense data")
				}

				if request.ChatID == firstOwner && (test.failures < 0 || len(firstAttempts) <= test.failures) {
					w.WriteHeader(test.status)
					if test.rateLimit {
						_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"private-provider-body","parameters":{"retry_after":1}}`))
					} else {
						_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": test.apiCode, "description": "private-provider-body"})
					}
					return
				}
				_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
			}))
			defer server.Close()

			client := testClient(server)
			model, calls := pipelineModel(t, pipelineScenario{})
			allowed := map[int64]bool{101: true, 202: true}
			orchestrator := conversation.New(model, postgres.NewTelegramStore(pool), allowed, time.UTC)
			var offsets []int64
			var pollTimes []time.Time
			api := loggingAPI{
				updates: func(_ context.Context, offset int64) ([]Update, error) {
					offsets = append(offsets, offset)
					pollTimes = append(pollTimes, time.Now())
					if offset == 42 {
						cancel()
						return nil, context.Canceled
					}
					return updates, nil
				},
				send: client.Send,
			}
			bot := NewBot(api, allowed, orchestrator.Handle)
			if err := bot.Run(ctx, 20*time.Millisecond); !errors.Is(err, context.Canceled) {
				t.Fatal("polling did not finish the batch")
			}

			expectedOffsets := []int64{0, 42}
			if !test.denied && (test.failures == 3 || test.processingFailure) {
				expectedOffsets = []int64{0, 0, 42}
				if len(pollTimes) >= 2 && pollTimes[1].Sub(pollTimes[0]) < 20*time.Millisecond {
					t.Fatal("retry did not wait for polling backoff")
				}
			}
			if !slices.Equal(offsets, expectedOffsets) {
				t.Fatalf("incorrect acknowledgement offsets: %v", offsets)
			}
			mutex.Lock()
			attempts := append([]time.Time(nil), firstAttempts...)
			sends := secondSends
			mutex.Unlock()
			if len(attempts) != test.firstSends || sends != 1 {
				t.Fatal("unexpected delivery attempts or blocked next recipient")
			}
			if test.rateLimit {
				for index := 1; index < len(attempts); index++ {
					if attempts[index].Sub(attempts[index-1]) < time.Second {
						t.Fatal("Telegram retry_after was ignored inside retries or polling")
					}
				}
			}

			expectedCalls := int32(2)
			expectedExpenses := 2
			if test.denied {
				expectedCalls, expectedExpenses = 1, 1
			} else if test.processingFailure {
				expectedCalls = 3
			}
			if calls.Load() != expectedCalls {
				t.Fatal("replayed delivery called the model or denied user reached it")
			}
			if err := bot.Process(context.Background(), updates[0]); err != nil {
				t.Fatal("duplicate committed update or denial could not be handled")
			}
			var expenses, results, deniedExpenses int
			err := pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM expenses),
				(SELECT count(*) FROM telegram_updates), (SELECT count(*) FROM expenses WHERE user_id=303)`).Scan(&expenses, &results, &deniedExpenses)
			if err != nil || expenses != expectedExpenses || results != expectedExpenses || deniedExpenses != 0 || calls.Load() != expectedCalls {
				t.Fatal("replay duplicated a mutation, lost an update, or bypassed ownership")
			}
			if strings.Contains(output.String(), "private-") || strings.Contains(output.String(), "bot-token") {
				t.Fatal("delivery diagnostics leaked private data")
			}
			foundOutcome := false
			for _, record := range decodeBotLogs(t, output) {
				if record["msg"] == "Telegram denial delivery failed" || record["msg"] == "Telegram response delivery failed" {
					foundOutcome = record["delivery_outcome"] != nil && record["retryable"] != nil
				}
			}
			if !test.processingFailure && !foundOutcome {
				t.Fatal("missing safe delivery outcome diagnostic")
			}
		})
	}
}
