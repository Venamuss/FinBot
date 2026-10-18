package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Venamuss/FinBot/internal/botresponse"
	"github.com/Venamuss/FinBot/internal/llm"
)

func TestUnsupportedCurrencyResponseIntegration(t *testing.T) {
	const want = "Поддерживаются только расходы в рублях (RUB). Укажите сумму в рублях."

	for _, text := range []string{"кофе 100 USD", "кофе 100 EUR", "кофе 100 THB", "кофе 100 PLN", "кофе 100$", "кофе 100€"} {
		for _, plan := range []struct{ name, args string }{
			{"create_expense", `{"amount":"100","spent_at":"today","comment":"кофе","currency":"RUB"}`},
			{"create_expense", `{"amount":100,"spent_at":"today"}`},
			{"clarify", `{"field":"unsupported_currency"}`},
			{"clarify", `{"field":"currency","amount":"100 THB"}`},
			{"update_expense", `{"amount":"100","currency":"RUB"}`},
			{"", ""},
		} {
			t.Run(text+"/"+plan.name+plan.args, func(t *testing.T) {
				workflow := newModificationWorkflow(t)
				workflow.send(101, "такси 350", "create_expense", `{"amount":"350","spent_at":"today","comment":"такси"}`)
				workflow.send(202, "еда 900", "create_expense", `{"amount":"900","spent_at":"today","comment":"еда"}`)
				before := expenseSnapshot(t, workflow)
				calls, mutations := workflow.calls.Load(), workflow.mutations.Load()
				input := text
				if plan.name == "update_expense" {
					input = "измени сумму на " + strings.TrimPrefix(text, "кофе ")
				}

				var reply string
				if plan.name == "" {
					workflow.plan = llm.Response{FinishReason: "stop"}
					reply = workflow.process(101, input, false)
				} else {
					reply = workflow.send(101, input, plan.name, plan.args)
				}
				if reply != want || expenseSnapshot(t, workflow) != before || workflow.mutations.Load() != mutations || workflow.calls.Load() != calls+1 {
					t.Fatalf("currency rejection changed data, called the model again or returned %q", reply)
				}

				if err := workflow.bot.Process(context.Background(), workflow.update); err != nil {
					t.Fatal(err)
				}
				if replay := <-workflow.replies; replay != want || workflow.calls.Load() != calls+1 || expenseSnapshot(t, workflow) != before {
					t.Fatal("currency rejection was not safely replayed")
				}

				reply = workflow.send(101, "еда 50", "create_expense", `{"amount":"50","spent_at":"today","comment":"еда"}`)
				created := workflow.item(3)
				if !strings.Contains(reply, "Расход добавлен") || workflow.count(101) != 2 || workflow.count(202) != 1 || created.Amount.String() != "50.00" || created.Comment == nil || *created.Comment != "еда" {
					t.Fatal("independent RUB request inherited rejected state")
				}
			})
		}
	}
}

func TestCurrencyErrorClassificationIntegration(t *testing.T) {
	for _, test := range []struct{ text, name, args, want string }{
		{"непонятный запрос", "clarify", `{"field":"unknown"}`, botresponse.InvalidRequest},
		{"кофе", "clarify", `{"field":"amount"}`, "Какую сумму записать?"},
		{"кофе 100 или 200", "clarify", `{"field":"amount"}`, "Какую сумму записать?"},
		{"кофе -100", "create_expense", `{"amount":"-100","spent_at":"today"}`, botresponse.InvalidAmount},
		{"кофе 1,001", "create_expense", `{"amount":"1.001","spent_at":"today"}`, botresponse.InvalidAmount},
		{"еда 50", "create_expense", `{"amount":"50","spent_at":"today","currency":"USD"}`, botresponse.InvalidRequest},
		{"кофе 100 RUB", "clarify", `{"field":"currency"}`, botresponse.InvalidRequest},
		{"SSD 500GB 4000", "create_expense", `{"amount":4000}`, botresponse.InvalidRequest},
		{"USB кабель 250", "create_expense", `{"amount":250}`, botresponse.InvalidRequest},
		{"кофе 100", "", "", botresponse.InvalidModelResponse},
	} {
		t.Run(test.text+"/"+test.name, func(t *testing.T) {
			workflow := newModificationWorkflow(t)
			var reply string
			if test.name == "" {
				workflow.plan = llm.Response{FinishReason: "stop"}
				reply = workflow.process(101, test.text, false)
			} else {
				reply = workflow.send(101, test.text, test.name, test.args)
			}
			if reply != test.want || workflow.mutations.Load() != 0 || workflow.count(101) != 0 {
				t.Fatalf("incorrect error classification: %q", reply)
			}
		})
	}

	t.Run("provider failure with currency evidence", func(t *testing.T) {
		workflow := newModificationWorkflow(t)
		workflow.modelStatus.Store(http.StatusServiceUnavailable)
		if reply := workflow.send(101, "кофе 100 THB", "create_expense", `{}`); reply != botresponse.Unavailable || workflow.mutations.Load() != 0 {
			t.Fatalf("provider failure misclassified: %q", reply)
		}
	})
}

func TestRUBAndQuantityCreationIntegration(t *testing.T) {
	for _, test := range []struct{ text, amount, comment string }{
		{"еда 50", "50", "еда"},
		{"магазин 123", "123", "магазин"},
		{"кофе 120,50", "120.50", "кофе"},
		{"кофе 100 руб", "100", "кофе"},
		{"кофе 100 RUB", "100", "кофе"},
		{"кофе 100 ₽", "100", "кофе"},
		{"SSD 500GB 4000", "4000", "SSD 500GB"},
		{"USB кабель 250", "250", "USB кабель"},
		{"вода 2 л 100 руб", "100", "вода 2 л"},
		{"бензин 40 л 2500 руб", "2500", "бензин 40 л"},
		{"купил 2 кофе за 500", "500", "купил 2 кофе"},
		{"3 бутылки воды за 150 ₽", "150", "3 бутылки воды"},
	} {
		t.Run(test.text, func(t *testing.T) {
			workflow := newModificationWorkflow(t)
			args, err := json.Marshal(map[string]string{"amount": test.amount, "spent_at": "today", "comment": test.comment})
			if err != nil {
				t.Fatal(err)
			}
			reply := workflow.send(101, test.text, "create_expense", string(args))
			item := workflow.item(1)
			amount := test.amount
			if !strings.Contains(amount, ".") {
				amount += ".00"
			}
			if !strings.Contains(reply, "Расход добавлен") || item.Amount.String() != amount || item.Comment == nil || *item.Comment != test.comment || workflow.count(101) != 1 {
				t.Fatal("valid RUB amount or quantity was changed")
			}
		})
	}
}
