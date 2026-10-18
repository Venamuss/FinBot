package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/botresponse"
	"github.com/Venamuss/FinBot/internal/conversation"
	"github.com/Venamuss/FinBot/internal/expense"
)

func TestExpenseDescriptionPersistenceIntegration(t *testing.T) {
	pool := oneConnectionPool(t, testPool(t))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Synthetic historical data is deliberately left unchanged by the fix.
	const historical = `INSERT INTO expenses (user_id, amount, category, spent_at, comment)
		VALUES (202, 123, 'другое', '2026-10-18', 'магазин 123') RETURNING id`
	var historicalID int64
	if err := pool.QueryRow(ctx, historical).Scan(&historicalID); err != nil {
		t.Fatal(err)
	}
	model := &integrationModel{pool: pool, name: "create_expense"}
	orchestrator := conversation.New(model, NewTelegramStore(pool), map[int64]bool{101: true, 202: true}, time.UTC)
	reference := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		text, expression, amount, comment string
	}{
		{"кофе 350", "350", "350", "кофе"},
		{"магазин 123", "123", "123", "магазин"},
		{"айфон 5 6050", "6050", "6050", "айфон 5"},
		{"айфон 13 про макс 40500", "40500", "40500", "айфон 13 про макс"},
		{"ламборджини 1233121", "1233121", "1233121", "ламборджини"},
		{"купил 2 кофе за 500", "за 500", "500", "купил 2 кофе"},
		{"такси за 450 рублей", "за 450 рублей", "450", "такси"},
		{"кофе 120,50", "120,50", "120.50", "кофе"},
	}
	var lastID int64
	for index, test := range cases {
		args, err := json.Marshal(map[string]string{
			"amount": test.amount, "amount_text": test.expression, "spent_at": "today", "comment": test.text,
		})
		if err != nil {
			t.Fatal(err)
		}
		model.args = string(args)
		reply, err := orchestrator.Handle(ctx, int64(index+1), 101, test.text, reference)
		if err != nil {
			t.Fatal(err)
		}
		var comment, amount string
		const latest = `SELECT id, comment, amount::text FROM expenses WHERE user_id = $1 ORDER BY id DESC LIMIT 1`
		if err := pool.QueryRow(ctx, latest, 101).Scan(&lastID, &comment, &amount); err != nil {
			t.Fatal(err)
		}
		wantAmount, err := expense.ParseAmount(test.amount)
		if err != nil {
			t.Fatal(err)
		}
		if comment != test.comment || amount != wantAmount.String() || !strings.Contains(reply, test.comment+" — <b>"+botresponse.Money(amount)+"</b>") {
			t.Fatal("incorrect persisted comment, amount or Telegram reply")
		}
		calls := model.calls
		replay, err := orchestrator.Handle(ctx, int64(index+1), 101, test.text, reference)
		if err != nil || replay != reply || model.calls != calls {
			t.Fatal("replay changed persisted outcome")
		}
	}
	model.name, model.args = "update_expense", `{"amount":"999"}`
	if _, err := orchestrator.Handle(ctx, 100, 101, "измени сумму на 999", reference); err != nil {
		t.Fatal(err)
	}
	var comment, amount string
	if err := pool.QueryRow(ctx, `SELECT comment, amount::text FROM expenses WHERE id = $1`, lastID).Scan(&comment, &amount); err != nil {
		t.Fatal(err)
	}
	if comment != "кофе" || amount != "999.00" {
		t.Fatal("amount update changed the saved description")
	}
	model.name, model.args = "delete_expense", `{}`
	if _, err := orchestrator.Handle(ctx, 101, 101, "удали последнюю", reference); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM expenses WHERE id = $1)`, lastID).Scan(&exists); err != nil || exists {
		t.Fatal("deletion failed")
	}
	if err := pool.QueryRow(ctx, `SELECT comment FROM expenses WHERE id = $1`, historicalID).Scan(&comment); err != nil || comment != "магазин 123" {
		t.Fatal("historical comment was rewritten")
	}
	model.name, model.args = "create_expense", `{"amount":"123","amount_text":"123","spent_at":"today","comment":"магазин 123"}`
	if _, err := orchestrator.Handle(ctx, 102, 202, "магазин 123", reference); err != nil {
		t.Fatal(err)
	}
	calls := model.calls
	reply, err := orchestrator.Handle(ctx, 102, 303, "магазин 123", reference)
	if err != nil || reply != conversation.Denied || model.calls != calls {
		t.Fatal("denied C accessed the model or B's stored response")
	}
	assertCounts(t, pool, 9, 11)
}
