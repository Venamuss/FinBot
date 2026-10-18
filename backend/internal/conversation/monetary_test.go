package conversation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Venamuss/FinBot/internal/expense"
)

func TestMonetaryDraftSourceValidation(t *testing.T) {
	for _, followup := range []bool{false, true} {
		orchestrator, model, store := setup(t)
		handle(t, orchestrator, model, 1, 101, "вода 2 л 100 руб", "clarify", `{"field":"spent_at","intent":"create_expense","amount":"2","amount_text":"2","comment":"вода 2 л"}`)
		name, args := "answer_clarification", `{"value":"today"}`
		if followup {
			name, args = "create_expense", `{"amount":"2","spent_at":"today","request_context":"followup"}`
		}
		reply := handle(t, orchestrator, model, 2, 101, "сегодня", name, args)
		if reply != clarificationQuestions["amount"] || len(store.repo.items) != 0 {
			t.Fatal("draft source price was not enforced on followup")
		}
		handle(t, orchestrator, model, 3, 101, "100", "answer_clarification", `{"value":"100"}`)
		item := store.repo.items[1]
		if item.Amount.String() != "100.00" || item.Comment == nil || *item.Comment != "вода 2 л" {
			t.Fatal("correct amount answer lost price or quantity")
		}
	}
}

func TestMonetaryCreationExamples(t *testing.T) {
	for _, test := range []struct{ text, amount, comment, date string }{
		{"еда 50", "50", "еда", "today"},
		{"магазин 123", "123", "магазин", "today"},
		{"кофе 120,50", "120.50", "кофе", "today"},
		{"такси 400 вчера", "400", "такси", "yesterday"},
		{"ламборджини 1233121", "1233121", "ламборджини", "today"},
		{"айфон 13 про макс 40500", "40500", "айфон 13 про макс", "today"},
		{"другое 120", "120", "другое", "today"},
		{"купил 2 кофе за 500", "500", "купил 2 кофе", "today"},
		{"кофе 100 руб", "100", "кофе", "today"},
		{"кофе 100 RUB", "100", "кофе", "today"},
		{"кофе 100 ₽", "100", "кофе", "today"},
		{"SSD 500GB 4000", "4000", "SSD 500GB", "today"},
		{"USB кабель 250", "250", "USB кабель", "today"},
		{"CAD программа за 4000", "4000", "CAD программа", "today"},
	} {
		t.Run(test.text, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			args, err := json.Marshal(map[string]string{"amount": test.amount, "comment": test.comment, "spent_at": test.date})
			if err != nil {
				t.Fatal(err)
			}
			reply := handle(t, orchestrator, model, 1, 101, test.text, "create_expense", string(args))
			item := store.repo.items[1]
			amount, err := expense.ParseAmount(test.amount)
			if err != nil {
				t.Fatal(err)
			}
			if len(store.repo.items) != 1 || item.Amount != amount || item.Comment == nil || *item.Comment != test.comment || !strings.Contains(reply, "Расход добавлен") {
				t.Fatalf("valid monetary request rejected or changed: %s", reply)
			}
			if test.date == "yesterday" && item.SpentAt != "2026-10-16" {
				t.Fatal("relative purchase date changed")
			}
		})
	}
}

func TestCurrencySourceContexts(t *testing.T) {
	for _, text := range []string{
		"кофе 100USD", "кофе USD100", "кофе 100 eur", "кофе 100 THB", "кофе thb 100", "кофе 100PLN",
		"кофе 120,50 KWD", "кофе 100 CHF", "кофе 100 XAU", "кофе 100 ZWG", "кофе 100 XCG",
		"кофе 100 USDT", "кофе 100 BTC", "кофе 100 ETH", "кофе 100 XYZ", "кофе 100XYZ.",
		"кофе 100$", "кофе $100", "кофе 100€", "кофе 100£", "кофе 100 тенге", "кофе 100 долларов",
	} {
		t.Run(text, func(t *testing.T) {
			if !unsupportedCurrency(text) {
				t.Fatal("explicit or ambiguous foreign monetary context was accepted")
			}
		})
	}
	for _, text := range []string{
		"кофе 100", "кофе 100 руб", "кофе 100 RUB", "кофе 100RUB", "кофе 100 ₽", "кофе 100р",
		"SSD 500GB 4000", "USB кабель 250", "CAD программа за 4000", "THB адаптер за 500", "другое 120",
	} {
		t.Run(text, func(t *testing.T) {
			if unsupportedCurrency(text) {
				t.Fatal("RUB default or description abbreviation treated as foreign currency")
			}
		})
	}
}

func TestMonetaryUpdateContexts(t *testing.T) {
	for _, text := range []string{"последнее было не 350, а 450", "измени сумму с 350 на 450", "измени сумму с 350 руб на 450 руб", "поменяй сумму такси на 600"} {
		t.Run(text, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			handle(t, orchestrator, model, 1, 101, "такси 350", "create_expense", `{"amount":"350","spent_at":"today","comment":"такси"}`)
			amount := "450"
			if strings.Contains(text, "600") {
				amount = "600"
			}
			handle(t, orchestrator, model, 2, 101, text, "update_expense", `{"amount":"`+amount+`"}`)
			if len(store.repo.items) != 1 || store.repo.items[1].Amount.String() != amount+".00" {
				t.Fatal("creation price validation interfered with old/replacement update amounts")
			}
		})
	}
}

func TestCurrencyFailureCancellationAndUpdate(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "такси 350", "create_expense", `{"amount":"350","spent_at":"today","comment":"такси"}`)
	reply := handle(t, orchestrator, model, 2, 101, "поменяй сумму такси на 100 THB", "update_expense", `{"amount":"100","currency":"RUB"}`)
	if reply != clarificationQuestions["currency"] || store.repo.items[1].Amount.String() != "350.00" {
		t.Fatal("source foreign currency bypassed update validation")
	}
	handle(t, orchestrator, model, 3, 101, "стоп", "cancel_request", `{}`)
	handle(t, orchestrator, model, 4, 101, "да", "update_expense", `{"amount":"100"}`)
	if store.repo.items[1].Amount.String() != "350.00" || orchestrator.states[101].pending != nil {
		t.Fatal("canceled currency draft mutated an expense")
	}
	handle(t, orchestrator, model, 5, 101, "еда 50", "create_expense", `{"amount":"50","spent_at":"today","comment":"еда"}`)
	if len(store.repo.items) != 2 || store.repo.items[2].Amount.String() != "50.00" {
		t.Fatal("canceled currency clarification affected independent creation")
	}
}

func TestExplicitPriceClarificationResolvesCompetingPrices(t *testing.T) {
	for _, answer := range []string{"100", "100 руб"} {
		t.Run(answer, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			reply := handle(t, orchestrator, model, 1, 101, "кофе 100 руб или 200 ₽", "create_expense", `{"amount":"100","spent_at":"today","comment":"кофе"}`)
			if reply != clarificationQuestions["amount"] || len(store.repo.items) != 0 {
				t.Fatal("competing prices did not require clarification")
			}
			handle(t, orchestrator, model, 2, 101, answer, "answer_clarification", `{"value":"200"}`)
			if len(store.repo.items) != 0 {
				t.Fatal("model invented a different clarification amount")
			}
			handle(t, orchestrator, model, 3, 101, answer, "answer_clarification", `{"value":"100"}`)
			if len(store.repo.items) != 1 || store.repo.items[1].Amount.String() != "100.00" {
				t.Fatal("explicit amount clarification did not resolve competing prices")
			}
		})
	}
}
