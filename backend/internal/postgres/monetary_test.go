package postgres

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/conversation"
	"github.com/Venamuss/FinBot/internal/expense"
)

func TestMonetarySourceRejectionIntegration(t *testing.T) {
	cases := []struct {
		text, amount, expression, question string
	}{
		{"вода 2 л 100 руб", "2", "2", "Какую сумму"},
		{"вода 2 л 100 руб", "2", "", "Какую сумму"},
		{"вода 2 л 100 RUB", "2", "", "Какую сумму"},
		{"вода 2 л 100 ₽", "2", "", "Какую сумму"},
		{"вода 2 л 2 руб", "2", "2", "Уточните действие"},
		{"вода 2 л 100 руб.", "2", "2", "Какую сумму"},
		{"вода 2 л 100 рублей", "2", "2", "Какую сумму"},
		{"вода 2 л 100 ₽", "2", "2", "Какую сумму"},
		{"вода 2 л 100 RUB", "2", "2", "Какую сумму"},
		{"вода 2 л 100р", "2", "2", "Какую сумму"},
		{"купил 2 кофе за 500", "2", "2", "Какую сумму"},
		{"3 бутылки воды за 150 ₽", "3", "3", "Какую сумму"},
		{"айфон 13 про макс 40500 RUB", "13", "13", "Какую сумму"},
		{"кофе 120,50 ₽", "120", "120", "Какую сумму"},
		{"кофе 100 руб или 200 RUB", "100", "100 руб", "Какую сумму"},
		{"кофе 100 руб 200 ₽", "200", "200 ₽", "Какую сумму"},
		{"кофе 100 USD", "100", "100", "только рубли"},
		{"кофе 100EUR", "100", "100", "только рубли"},
		{"кофе 100 THB", "100", "100", "только рубли"},
		{"кофе 100PLN", "100", "100", "только рубли"},
		{"кофе THB 100", "100", "100", "только рубли"},
		{"кофе 100$", "100", "100", "только рубли"},
		{"кофе 100€", "100", "100", "только рубли"},
		{"кофе 100 XYZ", "100", "100", "только рубли"},
	}
	for _, test := range cases {
		for _, currency := range []string{"", "RUB"} {
			t.Run(test.text+"/expression="+test.expression+"/model_currency="+currency, func(t *testing.T) {
				pool := oneConnectionPool(t, testPool(t))
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				repo := New(pool)
				for _, owner := range []string{"101", "202"} {
					if _, err := expense.NewService(repo).Create(ctx, expense.Input{UserID: owner, Amount: "350", Category: "транспорт", SpentAt: "2026-10-18"}); err != nil {
						t.Fatal(err)
					}
				}
				before, err := expense.NewService(repo).List(ctx, expense.Filter{Sort: "id", Order: "asc", Limit: 100})
				if err != nil {
					t.Fatal(err)
				}
				args := map[string]string{"amount": test.amount, "spent_at": "today", "comment": "вода 2 л"}
				if test.expression != "" {
					args["amount_text"] = test.expression
				}
				if currency != "" {
					args["currency"] = currency
				}
				encoded, err := json.Marshal(args)
				if err != nil {
					t.Fatal(err)
				}
				model := &integrationModel{pool: pool, name: "create_expense", args: string(encoded)}
				orchestrator := conversation.New(model, NewTelegramStore(pool), map[int64]bool{101: true, 202: true}, time.UTC)
				reference := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
				reply, err := orchestrator.Handle(ctx, 1, 101, test.text, reference)
				if err != nil || !strings.Contains(reply, test.question) || strings.Contains(reply, "Расход добавлен") {
					t.Fatalf("expected monetary clarification, got %q, error=%v", reply, err)
				}
				after, err := expense.NewService(repo).List(ctx, expense.Filter{Sort: "id", Order: "asc", Limit: 100})
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("rejected monetary input changed SQL expenses: %v", err)
				}
				calls := model.calls
				replay, err := orchestrator.Handle(ctx, 1, 101, test.text, reference)
				if err != nil || replay != reply || model.calls != calls {
					t.Fatal("rejection replay changed state or called model")
				}
				model.args = `{"amount":"50","amount_text":"50","spent_at":"today","comment":"еда"}`
				if _, err := orchestrator.Handle(ctx, 2, 101, "еда 50", reference); err != nil {
					t.Fatal(err)
				}
				latest, err := repo.LatestOwned(ctx, 101)
				if err != nil || latest.Amount.String() != "50.00" || latest.Comment == nil || *latest.Comment != "еда" {
					t.Fatalf("independent expense inherited rejected monetary input: %v", err)
				}
				assertCounts(t, pool, 3, 2)
			})
		}
	}
}
