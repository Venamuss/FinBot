package conversation

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
)

// Synthetic tool responses isolate orchestration from model interpretation.
func TestAmountRecognitionInvestigation(t *testing.T) {
	orchestrator, model, store := setup(t)
	reply := handle(t, orchestrator, model, 1, 101, "ламборджини 1233121", "clarify", `{"field":"amount"}`)
	if reply != "Какую сумму записать?" || len(store.repo.items) != 0 {
		t.Fatal("missing-amount question must originate in the model's clarify action")
	}
	reply = handle(t, orchestrator, model, 2, 101, "ламборджини 1233121", "create_expense", `{"amount":"1233121","category":"другое","spent_at":"today","comment":"ламборджини"}`)
	if !strings.Contains(reply, "1 233 121 ₽") || store.repo.items[1].Amount.String() != "1233121.00" {
		t.Fatal("valid large amount was rejected downstream")
	}
}

func TestUnsupportedCurrencyCannotBeSilentlyStoredAsRUB(t *testing.T) {
	orchestrator, model, store := setup(t)
	reply := handle(t, orchestrator, model, 1, 101, "магазин 123$", "create_expense", `{"amount":"123","category":"покупки","spent_at":"today"}`)
	if len(store.repo.items) != 0 || !strings.Contains(reply, "рублях") {
		t.Fatal("foreign currency was silently stored as RUB")
	}
}

func TestExpenseAmountExamples(t *testing.T) {
	cases := []struct {
		text, amount, category, description string
	}{
		{"кофе 350", "350", "еда", "кофе"},
		{"дом 12301200р", "12301200", "жильё", "дом"},
		{"ламба 12312312", "12312312", "другое", "ламба"},
		{"ламборджини 1233121", "1233121", "транспорт", "ламборджини"},
		{"ламборджини за 1233121 рублей", "1233121", "транспорт", "ламборджини"},
		{"магазин 123", "123", "покупки", "магазин"},
		{"покупка 120,50", "120.50", "покупки", "покупка"},
		{"такси 450.75", "450.75", "транспорт", "такси"},
		{"купил хлеб за 80 рублей", "80", "еда", "купил хлеб"},
		{"купил 2 кофе за 500", "500", "еда", "купил 2 кофе"},
		{"неведомая штуковина 98765432", "98765432", "другое", "неведомая штуковина"},
		{"покупка 999999999999,99 RUB", "999999999999.99", "покупки", "покупка"},
		{"европейский кофе 350", "350", "еда", "европейский кофе"},
		{"франкфурт 123", "123", "другое", "франкфурт"},
	}
	for _, test := range cases {
		t.Run(test.text, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			arguments, err := json.Marshal(map[string]string{
				"amount": test.amount, "category": test.category, "spent_at": "today", "comment": test.description,
			})
			if err != nil {
				t.Fatal(err)
			}
			reply := handle(t, orchestrator, model, 1, 101, test.text, "create_expense", string(arguments))
			if len(store.repo.items) != 1 {
				t.Fatalf("expense missing: %s", reply)
			}
			item := store.repo.items[1]
			amount, err := expense.ParseAmount(test.amount)
			if err != nil || item.Amount != amount || string(item.Category) != test.category || item.Comment == nil || *item.Comment != test.description {
				t.Fatal("amount, category or original description lost")
			}
			calls := model.calls
			replayed := handle(t, orchestrator, model, 1, 101, test.text, "create_expense", string(arguments))
			if replayed != reply || model.calls != calls || store.repo.sequence != 1 {
				t.Fatal("duplicate update called model or created another expense")
			}
		})
	}
}

func TestAmountClarificationFollowups(t *testing.T) {
	for _, text := range []string{"кофе", "кофе 200 или 300", "кофе 2026-10-18", "купил 2 кофе", "заказ ID 123"} {
		t.Run(text, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			reply := handle(t, orchestrator, model, 1, 101, text, "clarify", `{"field":"amount"}`)
			if reply != "Какую сумму записать?" || len(store.repo.items) != 0 {
				t.Fatal("expense created before clarification")
			}
			handle(t, orchestrator, model, 2, 101, "300", "create_expense", `{"amount":"300","category":"еда","spent_at":"today","comment":"кофе"}`)
			if store.repo.items[1].Amount.String() != "300.00" || !strings.Contains(model.request.Messages[0].Content, text) {
				t.Fatal("clarification context lost")
			}
		})
	}
}

func TestForeignCurrencyGuardsAndFollowups(t *testing.T) {
	for _, text := range []string{"магазин 123$", "магазин $123", "магазин 123 USD", "магазин 123usd", "магазин 123€", "магазин 123 EUR", "магазин 123 долларов", "магазин 123 фунта", "магазин 123£", "магазин 123¥", "магазин 123 BYN", "магазин 123 тенге", "магазин 123 ₿"} {
		t.Run(text, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			reply := handle(t, orchestrator, model, 1, 101, text, "create_expense", `{"amount":"123","category":"покупки","spent_at":"today","currency":"RUB","comment":"магазин"}`)
			if !strings.Contains(reply, "рублях") || len(store.repo.items) != 0 {
				t.Fatal("model bypassed source currency validation")
			}
			handle(t, orchestrator, model, 2, 101, "да", "clarify", `{"field":"amount"}`)
			reply = handle(t, orchestrator, model, 3, 101, "123", "create_expense", `{"amount":"123","category":"покупки","spent_at":"today","currency":"RUB","request_context":"followup"}`)
			if len(store.repo.items) != 0 || !strings.Contains(reply, "рублях") {
				t.Fatal("unresolved currency was silently changed to default RUB")
			}
			handle(t, orchestrator, model, 4, 101, "123 руб", "create_expense", `{"amount":"123","category":"покупки","spent_at":"today","currency":"RUB","comment":"магазин","request_context":"followup"}`)
			if len(store.repo.items) != 1 || store.repo.items[1].Amount.String() != "123.00" || orchestrator.states[101].pending != nil {
				t.Fatal("RUB correction failed")
			}
			if !strings.Contains(model.request.Messages[0].Content, text) || !strings.Contains(model.request.Messages[0].Content, `"pending_field":"currency"`) {
				t.Fatal("currency clarification lost original context")
			}
		})
	}
}

func TestCurrencyArgumentsAndUpdates(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "кофе 350 RUB", "create_expense", `{"amount":"350","category":"еда","spent_at":"today","currency":"RUB"}`)
	reply := handle(t, orchestrator, model, 2, 101, "измени сумму на 400$", "update_expense", `{"amount":"400"}`)
	if !strings.Contains(reply, "рублях") || store.repo.items[1].Amount.String() != "350.00" {
		t.Fatal("foreign currency update was applied")
	}
	handle(t, orchestrator, model, 3, 101, "400 ₽", "update_expense", `{"amount":"400","currency":"RUB"}`)
	if store.repo.items[1].Amount.String() != "400.00" {
		t.Fatal("RUB update correction failed")
	}
	for index, currency := range []string{"USD", "EUR", "XYZ"} {
		args, err := json.Marshal(map[string]string{"amount": "123", "category": "другое", "spent_at": "today", "currency": currency})
		if err != nil {
			t.Fatal(err)
		}
		reply = handle(t, orchestrator, model, int64(index+4), 202, "синтетический запрос", "create_expense", string(args))
		if !strings.Contains(reply, "рублях") || len(store.repo.items) != 1 {
			t.Fatal("unsupported tool currency was accepted")
		}
	}
}

func TestMalformedMonetaryArgumentsDoNotMutate(t *testing.T) {
	for _, amount := range []string{`350`, `null`, `""`, `"120,50"`, `"1.001"`, `"123$"`, `"-5"`, `"0"`, `"01"`, `"1e3"`, `"NaN"`, `"999999999999.999"`, `"1000000000000"`} {
		t.Run(amount, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			handle(t, orchestrator, model, 1, 101, "синтетический запрос", "create_expense", `{"amount":`+amount+`,"category":"другое","spent_at":"today"}`)
			if len(store.repo.items) != 0 {
				t.Fatal("malformed amount bypassed validation")
			}
		})
	}
}

func TestAmountInstructionsAndToolContract(t *testing.T) {
	orchestrator, _, _ := setup(t)
	request := orchestrator.request("синтетический запрос", time.Now(), state{})
	for _, instruction := range []string{"<expense description> <amount>", "ламборджини 1233121", "120.50", "купил 2 кофе", "кофе 200 или 300", "otherwise use другое", "RUB only"} {
		if !strings.Contains(request.Messages[0].Content, instruction) {
			t.Fatalf("missing model guidance: %s", instruction)
		}
	}
	for _, definition := range request.Tools {
		if definition.Function.Name != "create_expense" {
			continue
		}
		// comment is nullable, so inspect the amount property independently.
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(definition.Function.Parameters, &fields); err != nil {
			t.Fatal(err)
		}
		var properties map[string]json.RawMessage
		if err := json.Unmarshal(fields["properties"], &properties); err != nil {
			t.Fatal(err)
		}
		var amount struct {
			Type    string
			Pattern string
		}
		if err := json.Unmarshal(properties["amount"], &amount); err != nil {
			t.Fatal(err)
		}
		pattern, err := regexp.Compile(amount.Pattern)
		if err != nil || amount.Type != "string" {
			t.Fatal("amount must be an exact decimal string")
		}
		for _, value := range []string{"1233121", "120.50", "999999999999.99"} {
			if !pattern.MatchString(value) {
				t.Fatal("schema rejects a valid monetary value")
			}
		}
		for _, value := range []string{"120,50", "1.001", "123$", "1000000000000"} {
			if pattern.MatchString(value) {
				t.Fatal("schema permits malformed or oversized amount")
			}
		}
	}
}

func TestIndependentExpensesAfterCurrencyError(t *testing.T) {
	cases := []struct {
		text, amount, description string
	}{
		{"магазин 123", "123", "магазин"},
		{"магазин 120", "120", "магазин"},
		{"еда 50", "50", "еда"},
		{"кофе 120,50", "120.50", "кофе"},
		{"кофе 450.75", "450.75", "кофе"},
		{"дом 500р", "500", "дом"},
		{"покупка 500 ₽", "500", "покупка"},
		{"покупка 500 руб.", "500", "покупка"},
		{"покупка 500 рублей", "500", "покупка"},
		{"ламборджини 1233121", "1233121", "ламборджини"},
		{"неведомая покупка 999999999999,99", "999999999999.99", "неведомая покупка"},
	}
	for _, test := range cases {
		t.Run(test.text, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			handle(t, orchestrator, model, 1, 101, "магазин 123$", "create_expense", `{"amount":"123","category":"покупки","spent_at":"today"}`)
			arguments, err := json.Marshal(map[string]string{
				"amount": test.amount, "category": "другое", "spent_at": "today", "comment": test.description,
			})
			if err != nil {
				t.Fatal(err)
			}
			reply := handle(t, orchestrator, model, 2, 101, test.text, "create_expense", string(arguments))
			if len(store.repo.items) != 1 {
				t.Fatalf("independent expense rejected: %s", reply)
			}
			item := store.repo.items[1]
			amount, err := expense.ParseAmount(test.amount)
			if err != nil || item.Amount != amount || item.Comment == nil || *item.Comment != test.description || item.UserID != 101 {
				t.Fatal("independent expense inherited data from the rejected request")
			}
			if orchestrator.states[101].pending != nil {
				t.Fatal("successful independent expense retained currency clarification")
			}
			calls := model.calls
			handle(t, orchestrator, model, 2, 101, test.text, "create_expense", string(arguments))
			if store.repo.sequence != 1 || model.calls != calls {
				t.Fatal("independent expense replay created a duplicate")
			}
		})
	}
}

func TestCurrencyRequestContextKeepsTheCorrectDate(t *testing.T) {
	for _, scope := range []string{"", "new", "followup"} {
		t.Run("context="+scope, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			original := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
			model.response = tool("create_expense", `{"amount":"123","category":"покупки","spent_at":"today"}`)
			if _, err := orchestrator.Handle(context.Background(), 1, 101, "магазин 123 USD", original); err != nil {
				t.Fatal(err)
			}
			arguments := map[string]string{"amount": "50", "category": "еда", "spent_at": "today", "comment": "еда", "currency": "RUB"}
			if scope != "" {
				arguments["request_context"] = scope
			}
			text := "еда 50"
			expectedDate := expense.Date("2026-10-17")
			if scope == "followup" {
				text = "50 руб."
				expectedDate = "2026-10-16"
			}
			encoded, err := json.Marshal(arguments)
			if err != nil {
				t.Fatal(err)
			}
			model.response = tool("create_expense", string(encoded))
			if _, err := orchestrator.Handle(context.Background(), 2, 101, text, original.Add(24*time.Hour)); err != nil {
				t.Fatal(err)
			}
			if len(store.repo.items) != 1 || store.repo.items[1].SpentAt != expectedDate {
				t.Fatal("new request or follow-up used the wrong calendar reference")
			}
		})
	}
}

func TestIndependentClarificationReplacesCurrencyQuestion(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "магазин 123 EUR", "create_expense", `{"amount":"123","category":"покупки","spent_at":"today"}`)
	reply := handle(t, orchestrator, model, 2, 101, "кофе", "clarify", `{"field":"amount","request_context":"new"}`)
	if reply != "Какую сумму записать?" || orchestrator.states[101].pending.text != "кофе" || len(store.repo.items) != 0 {
		t.Fatal("independent missing-amount request inherited a currency question")
	}
	handle(t, orchestrator, model, 3, 101, "120,50", "create_expense", `{"amount":"120.50","category":"еда","spent_at":"today","comment":"кофе","request_context":"followup"}`)
	if len(store.repo.items) != 1 || store.repo.items[1].Amount.String() != "120.50" || orchestrator.states[101].pending != nil {
		t.Fatal("amount clarification did not recover after a currency error")
	}
	handle(t, orchestrator, model, 4, 101, "новая покупка 50 долларов или рублей", "clarify", `{"field":"currency","request_context":"new"}`)
	if len(store.repo.items) != 1 || orchestrator.states[101].pending.field != "currency" {
		t.Fatal("genuinely ambiguous currency bypassed clarification")
	}
	handle(t, orchestrator, model, 5, 101, "рублей", "create_expense", `{"amount":"50","category":"покупки","spent_at":"today","comment":"новая покупка","request_context":"followup"}`)
	if len(store.repo.items) != 2 || store.repo.items[2].Amount.String() != "50.00" {
		t.Fatal("currency clarification could not be resolved explicitly")
	}
}

func TestInvalidRequestContextCannotDiscardPendingState(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "магазин 123$", "create_expense", `{"amount":"123","category":"покупки","spent_at":"today"}`)
	pending := orchestrator.states[101].pending
	for index, value := range []string{`"other"`, `true`, `null`, `""`} {
		reply := handle(t, orchestrator, model, int64(index+2), 101, "еда 50", "create_expense", `{"amount":"50","category":"еда","spent_at":"today","request_context":`+value+`}`)
		if reply != invalidRequest || len(store.repo.items) != 0 || orchestrator.states[101].pending != pending {
			t.Fatal("invalid context mutated expenses or discarded clarification")
		}
	}
}
