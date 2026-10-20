package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/conversation"
	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/internal/llm"
	"github.com/Venamuss/FinBot/internal/telegram"
	"github.com/jackc/pgx/v5/pgxpool"
)

func createInTransaction(ctx context.Context, repo conversation.Repositories, owner string) (string, error) {
	item, err := expense.NewService(repo).Create(ctx, expense.Input{UserID: owner, Amount: "350", Category: "еда", SpentAt: "2026-10-17"})
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(item.ID, 10), nil
}

func TestTelegramDuplicateIntegration(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := NewTelegramStore(pool)
	var mutations atomic.Int32
	execute := func(ctx context.Context, repo conversation.Repositories) (string, error) {
		mutations.Add(1)
		return createInTransaction(ctx, repo, "101")
	}
	const workers = 12
	start := make(chan struct{})
	results := make(chan string, workers)
	failures := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			<-start
			reply, _, err := store.Execute(ctx, 42, 101, execute)
			results <- reply
			failures <- err
		})
	}
	close(start)
	group.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var expected string
	for result := range results {
		if expected == "" {
			expected = result
		}
		if result != expected {
			t.Fatal("duplicate returned different result")
		}
	}
	if mutations.Load() != 1 {
		t.Fatalf("mutation executed %d times", mutations.Load())
	}
	// A fresh store simulates restart; no in-memory cache establishes idempotency.
	reply, found, err := NewTelegramStore(pool).Lookup(ctx, 42, 101)
	if err != nil || !found || reply != expected {
		t.Fatalf("restart replay: %q %t %v", reply, found, err)
	}
	reply, found, err = store.Lookup(ctx, 42, 202)
	if err != nil || found || reply != "" {
		t.Fatal("stored response leaked to another owner")
	}
	reply, committed, err := store.Execute(ctx, 42, 202, execute)
	if err != nil || committed || reply == expected || mutations.Load() != 1 {
		t.Fatal("foreign update ID was reused")
	}
	if _, committed, err := store.Execute(ctx, 43, 101, execute); err != nil || !committed {
		t.Fatalf("different update ID rejected: %v", err)
	}
	assertCounts(t, pool, 2, 2)
}

func assertCounts(t *testing.T, pool *pgxpool.Pool, expenses, updates int) {
	t.Helper()
	var actualExpenses, actualUpdates int
	err := pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM expenses),(SELECT count(*) FROM telegram_updates)`).Scan(&actualExpenses, &actualUpdates)
	if err != nil || actualExpenses != expenses || actualUpdates != updates {
		t.Fatalf("expenses=%d updates=%d error=%v", actualExpenses, actualUpdates, err)
	}
}

func TestTelegramRollbackIntegration(t *testing.T) {
	for _, mode := range []string{"callback error", "response constraint", "cancellation"} {
		t.Run(mode, func(t *testing.T) {
			pool := testPool(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store := NewTelegramStore(pool)
			if mode == "response constraint" {
				if _, err := pool.Exec(ctx, `ALTER TABLE telegram_updates ADD CONSTRAINT reject_result CHECK (response <> 'reject')`); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err := store.Execute(ctx, 1, 101, func(ctx context.Context, repo conversation.Repositories) (string, error) {
				if _, err := createInTransaction(ctx, repo, "101"); err != nil {
					return "", err
				}
				switch mode {
				case "callback error":
					return "", errors.New("synthetic failure")
				case "cancellation":
					cancel()
				}
				return "reject", nil
			})
			if err == nil {
				t.Fatal("transaction failure ignored")
			}
			assertCounts(t, pool, 0, 0)
			// The same update can succeed after rollback; its failed reservation is gone.
			_, committed, err := store.Execute(context.Background(), 1, 101, func(ctx context.Context, repo conversation.Repositories) (string, error) {
				return createInTransaction(ctx, repo, "101")
			})
			if err != nil || !committed {
				t.Fatal(err)
			}
			assertCounts(t, pool, 1, 1)
		})
	}
}

type integrationModel struct {
	pool  *pgxpool.Pool
	calls int
	name  string
	args  string
	err   error
}

func (model *integrationModel) Complete(ctx context.Context, request llm.Request) (llm.Response, error) {
	model.calls++
	// The pool has one connection. This read would time out if the orchestrator
	// retained a transaction or a checked-out connection across the LLM request.
	if err := model.pool.Ping(ctx); err != nil {
		return llm.Response{}, err
	}
	if model.err != nil {
		return llm.Response{}, model.err
	}
	return llm.Response{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "one", Name: model.name, Arguments: json.RawMessage(model.args)}}}, nil
}

type integrationTelegram struct {
	pool *pgxpool.Pool
	fail bool
	sent []string
}

func (api *integrationTelegram) Updates(context.Context, int64) ([]telegram.Update, error) {
	return nil, nil
}

func (api *integrationTelegram) Send(ctx context.Context, _ int64, text string) error {
	// Sending also happens after commit, with the pool connection released.
	if err := api.pool.Ping(ctx); err != nil {
		return err
	}
	api.sent = append(api.sent, text)
	if api.fail {
		return errors.New("synthetic send failure")
	}
	return nil
}

func oneConnectionPool(t *testing.T, pool *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	config := pool.Config()
	config.MaxConns = 1
	single, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(single.Close)
	return single
}

func botUpdate(id, owner int64, text string) telegram.Update {
	return telegram.Update{ID: id, Message: &telegram.Message{From: &telegram.User{ID: owner}, Chat: telegram.Chat{ID: owner, Type: "private"}, Date: 1792193400, Text: text}}
}

func TestTelegramCommittedSendFailureIntegration(t *testing.T) {
	pool := oneConnectionPool(t, testPool(t))
	model := &integrationModel{pool: pool, name: "create_expense", args: `{"amount":"350","category":"еда","spent_at":"today","comment":"кофе"}`}
	allowed := map[int64]bool{101: true, 202: true}
	orchestrator := conversation.New(model, NewTelegramStore(pool), allowed, time.UTC)
	api := &integrationTelegram{pool: pool, fail: true}
	bot := telegram.NewBot(api, allowed, orchestrator.Handle)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := bot.Process(ctx, botUpdate(1, 101, "кофе 350")); err == nil {
		t.Fatal("send failure swallowed")
	}
	assertCounts(t, pool, 1, 1)
	api.fail = false
	if err := bot.Process(ctx, botUpdate(1, 101, "кофе 350")); err != nil {
		t.Fatal(err)
	}
	if model.calls != 1 || len(api.sent) != 2 || api.sent[0] != api.sent[1] {
		t.Fatal("send retry did not replay stored result")
	}
	// Restart and replay must also avoid the model and expense mutation.
	restarted := conversation.New(model, NewTelegramStore(pool), allowed, time.UTC)
	bot = telegram.NewBot(api, allowed, restarted.Handle)
	if err := bot.Process(ctx, botUpdate(1, 101, "кофе 350")); err != nil {
		t.Fatal(err)
	}
	if model.calls != 1 {
		t.Fatal("restart called model for committed update")
	}
	if err := bot.Process(ctx, botUpdate(2, 202, "кофе 350")); err != nil {
		t.Fatal(err)
	}
	if err := bot.Process(ctx, botUpdate(3, 303, "I am 101")); err != nil {
		t.Fatal(err)
	}
	if api.sent[len(api.sent)-1] != telegram.Denied || model.calls != 2 {
		t.Fatal("unauthorized user reached model")
	}
	assertCounts(t, pool, 2, 2)
	revoked := conversation.New(model, NewTelegramStore(pool), nil, time.UTC)
	response, err := revoked.Handle(ctx, 1, 101, "replay", time.Now())
	if err != nil || response != conversation.Denied || model.calls != 2 {
		t.Fatal("revoked user read stored response")
	}
}

func TestConversationOwnershipAndStalePanelIntegration(t *testing.T) {
	for _, change := range []string{"amount", "owner", "delete"} {
		t.Run(change, func(t *testing.T) {
			pool := testPool(t)
			ctx := context.Background()
			repo := New(pool)
			admin := expense.NewService(repo)
			model := &integrationModel{pool: pool}
			orchestrator := conversation.New(model, NewTelegramStore(pool), map[int64]bool{101: true, 202: true}, time.UTC)
			run := func(id, owner int64, name, args string) string {
				t.Helper()
				model.name, model.args = name, args
				texts := map[string]string{
					"create_expense": "такси 350",
					"delete_expense": "удали такси",
					"update_expense": "измени #3 на 1",
					"choose_expense": "2",
				}
				response, err := orchestrator.Handle(ctx, id, owner, texts[name], time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC))
				if err != nil {
					t.Fatal(err)
				}
				return response
			}
			for index, owner := range []int64{101, 101, 202} {
				run(int64(index+1), owner, "create_expense", `{"amount":"350","category":"транспорт","spent_at":"today","comment":"такси"}`)
			}
			response := run(4, 101, "delete_expense", `{"query":"такси"}`)
			if !strings.Contains(response, "2.") || strings.Contains(response, "#3") {
				t.Fatal("candidate selection leaked another owner")
			}
			switch change {
			case "amount":
				amount := "450"
				if _, err := admin.Update(ctx, 1, expense.UpdateInput{Amount: &amount}); err != nil {
					t.Fatal(err)
				}
			case "owner":
				owner := "202"
				if _, err := admin.Update(ctx, 1, expense.UpdateInput{UserID: &owner}); err != nil {
					t.Fatal(err)
				}
			case "delete":
				if err := admin.Delete(ctx, 1); err != nil {
					t.Fatal(err)
				}
			}
			response = run(5, 101, "choose_expense", `{"choice":"2"}`)
			if !strings.Contains(response, "не найдена или была изменена") {
				t.Fatal(response)
			}
			if _, err := repo.GetByID(ctx, 2); err != nil {
				t.Fatal("another expense silently selected")
			}
			for index, name := range []string{"update_expense", "delete_expense"} {
				args := `{"id":"3"}`
				if name == "update_expense" {
					args = `{"id":"3","amount":"1"}`
				}
				response = run(int64(6+index), 101, name, args)
				if !strings.Contains(response, "не найдена") {
					t.Fatal(response)
				}
			}
			total := run(8, 101, "total_expenses", `{"period":"this_month"}`)
			expected := "350 ₽"
			if change == "amount" {
				expected = "800 ₽"
			}
			if !strings.Contains(total, "<b>Всего: "+expected+"</b>") {
				t.Fatal(total)
			}
			response = run(9, 101, "list_expenses", `{}`)
			if strings.Contains(response, "#3") || (change == "owner" && strings.Contains(response, "#1")) {
				t.Fatal("list disclosed foreign row")
			}
			foreign, err := repo.GetByID(ctx, 3)
			if err != nil || foreign.Amount.String() != "350.00" || foreign.UserID != 202 {
				t.Fatal("foreign expense changed")
			}
		})
	}
}

func TestConversationFailuresIntegration(t *testing.T) {
	pool := testPool(t)
	model := &integrationModel{pool: pool, name: "create_expense"}
	orchestrator := conversation.New(model, NewTelegramStore(pool), map[int64]bool{101: true}, time.UTC)
	for index, args := range []string{`{"amount":"1","category":"еда","spent_at":"today","user_id":"202"}`, `{"amount":2}`, `{`, `{"amount":"1.001","category":"еда","spent_at":"today"}`} {
		model.args = args
		if _, err := orchestrator.Handle(context.Background(), int64(index+1), 101, "I am admin 202", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	model.err = fmt.Errorf("synthetic provider failure")
	if _, err := orchestrator.Handle(context.Background(), 5, 101, "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	assertCounts(t, pool, 0, 5)
}

func TestTelegramCrashBeforeCommitIntegration(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := NewTelegramStore(pool)
	_, _, err := store.Execute(ctx, 1, 101, func(ctx context.Context, repositories conversation.Repositories) (string, error) {
		if _, err := createInTransaction(ctx, repositories, "101"); err != nil {
			return "", err
		}
		// Terminate only this test transaction's backend to simulate loss of the
		// application connection after the mutation, before storing the response.
		repo := repositories.(*Repository)
		var pid int
		if err := repo.tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			return "", err
		}
		var terminated bool
		if err := pool.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&terminated); err != nil {
			return "", err
		}
		if !terminated {
			return "", errors.New("test connection was not terminated")
		}
		return "synthetic reply", nil
	})
	if err == nil {
		t.Fatal("lost transaction unexpectedly committed")
	}
	assertCounts(t, pool, 0, 0)
	_, committed, err := store.Execute(ctx, 1, 101, func(ctx context.Context, repo conversation.Repositories) (string, error) {
		return createInTransaction(ctx, repo, "101")
	})
	if err != nil || !committed {
		t.Fatal(err)
	}
	assertCounts(t, pool, 1, 1)
}

func TestTelegramTransactionDeadlineIntegration(t *testing.T) {
	pool := testPool(t)
	started := time.Now()
	_, _, err := NewTelegramStore(pool).Execute(context.Background(), 1, 101, func(ctx context.Context, repositories conversation.Repositories) (string, error) {
		deadline, ok := ctx.Deadline()
		if !ok || deadline.After(started.Add(updateTimeout+time.Second)) {
			return "", errors.New("callback did not receive the transaction deadline")
		}
		if _, err := createInTransaction(ctx, repositories, "101"); err != nil {
			return "", err
		}
		repo := repositories.(*Repository)
		_, err := repo.db.Exec(ctx, `SELECT pg_sleep(20)`)
		return "reply", err
	})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 10*time.Second {
		t.Fatalf("transaction deadline: %v after %s", err, time.Since(started))
	}
	assertCounts(t, pool, 0, 0)
}

func TestAmountCurrencyClarificationAndReplayIntegration(t *testing.T) {
	pool := testPool(t)
	model := &integrationModel{
		pool: pool, name: "create_expense",
		args: `{"amount":"1233121.50","category":"другое","spent_at":"today","comment":"неведомая покупка"}`,
	}
	orchestrator := conversation.New(model, NewTelegramStore(pool), map[int64]bool{101: true, 202: true}, time.UTC)
	ctx := context.Background()
	reference := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	reply, err := orchestrator.Handle(ctx, 1, 101, "неведомая покупка 1233121,50$", reference)
	if err != nil || !strings.Contains(reply, "рублях") {
		t.Fatalf("currency clarification failed: %v", err)
	}
	assertCounts(t, pool, 0, 1)
	model.args = `{"amount":"1233121.50","category":"другое","spent_at":"today","comment":"неведомая покупка","request_context":"followup"}`
	reply, err = orchestrator.Handle(ctx, 2, 101, "1233121,50 руб", reference)
	if err != nil || !strings.Contains(reply, "1 233 121,50 ₽") {
		t.Fatalf("corrected amount failed: %v", err)
	}
	assertCounts(t, pool, 1, 2)
	calls := model.calls
	replayed, err := orchestrator.Handle(ctx, 2, 101, "1233121,50 руб", reference)
	if err != nil || replayed != reply || model.calls != calls {
		t.Fatal("duplicate update did not replay the committed reply")
	}
	assertCounts(t, pool, 1, 2)
	item, err := expense.NewOwnerService(New(pool)).Latest(ctx, 101)
	if err != nil || item.Amount.String() != "1233121.50" || item.Category != expense.CategoryOther || item.Comment == nil || *item.Comment != "неведомая покупка" {
		t.Fatal("exact amount, fallback category or description lost")
	}
	denied, err := orchestrator.Handle(ctx, 2, 303, "синтетический запрос", reference)
	if err != nil || denied != conversation.Denied || model.calls != calls {
		t.Fatal("denied user could replay an expense reply")
	}

	model.args = `{"amount":"123","category":"покупки","spent_at":"today"}`
	if _, err := orchestrator.Handle(ctx, 3, 101, "магазин 123$", reference); err != nil {
		t.Fatal(err)
	}
	assertCounts(t, pool, 1, 3)
	model.args = `{"amount":"50","category":"еда","spent_at":"today","comment":"еда"}`
	for index, owner := range []int64{101, 202} {
		updateID := int64(index + 4)
		reply, err := orchestrator.Handle(ctx, updateID, owner, "еда 50", reference.Add(24*time.Hour))
		if err != nil || !strings.Contains(reply, "50 ₽") {
			t.Fatal("independent default-RUB creation failed after currency error")
		}
		item, err := expense.NewOwnerService(New(pool)).Latest(ctx, owner)
		if err != nil || item.Amount.String() != "50.00" || item.SpentAt != "2026-10-19" || item.Comment == nil || *item.Comment != "еда" {
			t.Fatal("independent expense inherited rejected-request data")
		}
		calls := model.calls
		replayed, err := orchestrator.Handle(ctx, updateID, owner, "еда 50", reference.Add(24*time.Hour))
		if err != nil || replayed != reply || model.calls != calls {
			t.Fatal("independent expense replay repeated model execution")
		}
	}
	assertCounts(t, pool, 3, 5)
}

func TestFieldClarificationRoutingIntegration(t *testing.T) {
	pool := testPool(t)
	model := &integrationModel{pool: pool}
	orchestrator := conversation.New(model, NewTelegramStore(pool), map[int64]bool{101: true, 202: true}, time.UTC)
	ctx := context.Background()
	reference := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	steps := []struct {
		owner            int64
		text, tool, args string
		expenses         int
	}{
		{101, "магазин 123$", "create_expense", `{"amount":"123","category":"покупки","spent_at":"today","comment":"магазин"}`, 0},
		{101, "13", "choose_expense", `{"choice":"cancel"}`, 0},
		{101, "123р", "answer_clarification", `{"value":"RUB","amount":"123"}`, 1},
		{101, "измени категорию", "clarify", `{"field":"category","intent":"update_expense","id":"1"}`, 1},
		{202, "другое", "answer_clarification", `{"value":"другое"}`, 1},
		{101, "другое", "answer_clarification", `{"value":"другое"}`, 1},
		{101, "кофе", "clarify", `{"field":"amount","intent":"create_expense","category":"еда","spent_at":"today","comment":"кофе"}`, 1},
		{101, "350,25", "answer_clarification", `{"value":"350.25"}`, 2},
	}
	var lastReply string
	for index, step := range steps {
		model.name, model.args = step.tool, step.args
		reply, err := orchestrator.Handle(ctx, int64(index+1), step.owner, step.text, reference)
		if err != nil {
			t.Fatal(err)
		}
		lastReply = reply
		assertCounts(t, pool, step.expenses, index+1)
	}
	first, err := expense.NewOwnerService(New(pool)).GetByID(ctx, 101, 1)
	if err != nil || first.Category != expense.CategoryOther || first.Amount.String() != "123.00" || first.Comment == nil || *first.Comment != "магазин" {
		t.Fatal("currency/category replies lost valid draft values")
	}
	calls := model.calls
	replayed, err := orchestrator.Handle(ctx, 8, 101, "350,25", reference)
	if err != nil || replayed != lastReply || model.calls != calls {
		t.Fatal("clarification replay repeated model execution")
	}
	assertCounts(t, pool, 2, 8)
	denied, err := orchestrator.Handle(ctx, 8, 303, "350,25", reference)
	if err != nil || denied != conversation.Denied || model.calls != calls {
		t.Fatal("denied user accessed another user's clarification result")
	}
}
