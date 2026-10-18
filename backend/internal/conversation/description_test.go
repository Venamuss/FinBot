package conversation

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/botresponse"
)

func TestCreationDescriptionSeparation(t *testing.T) {
	cases := []struct {
		text, expression, amount, description string
	}{
		{"кофе 350", "350", "350", "кофе"},
		{"глаза 500", "500", "500", "глаза"},
		{"магазин 123", "123", "123", "магазин"},
		{"айфон 5 6050", "6050", "6050", "айфон 5"},
		{"айфон 13 про макс 40500", "40500", "40500", "айфон 13 про макс"},
		{"ламборджини 1233121", "1233121", "1233121", "ламборджини"},
		{"купил 2 кофе за 500", "за 500", "500", "купил 2 кофе"},
		{"такси за 450 рублей", "за 450 рублей", "450", "такси"},
		{"кофе 120,50", "120,50", "120.50", "кофе"},
		{"кофе 120.50", "120.50", "120.50", "кофе"},
		{"кофе 350 ₽", "350 ₽", "350", "кофе"},
		{"кофе 350 RUB", "350 RUB", "350", "кофе"},
		{"кофе 350р", "350р", "350", "кофе"},
		{"кофе 350 руб.", "350 руб.", "350", "кофе"},
		{"кофе 999999999999,99 рублей", "999999999999,99 рублей", "999999999999.99", "кофе"},
		{"за 500 рублей купил 2 кофе", "за 500 рублей", "500", "купил 2 кофе"},
		{"айфон 5 за 5 рублей", "за 5 рублей", "5", "айфон 5"},
	}
	for _, test := range cases {
		for _, contaminated := range []bool{false, true} {
			comment := test.description
			if contaminated {
				comment = test.text
			}
			t.Run(test.text+"/"+comment, func(t *testing.T) {
				orchestrator, model, store := setup(t)
				arguments, err := json.Marshal(map[string]string{
					"amount": test.amount, "amount_text": test.expression, "spent_at": "today", "comment": comment,
				})
				if err != nil {
					t.Fatal(err)
				}
				reply := handle(t, orchestrator, model, 1, 101, test.text, "create_expense", string(arguments))
				item := store.repo.items[1]
				if item.Comment == nil || *item.Comment != test.description || reply != botresponse.Created(item) || model.calls != 1 {
					t.Fatalf("incorrect stored description or reply: %s", reply)
				}
				if !strings.Contains(reply, test.description+" — <b>"+botresponse.Money(item.Amount.String())+"</b>") {
					t.Fatal("Telegram output did not use separated fields")
				}
				if replay := handle(t, orchestrator, model, 1, 101, test.text, "create_expense", string(arguments)); replay != reply || model.calls != 1 {
					t.Fatal("replay changed reply or called the model again")
				}
			})
		}
	}
}

func TestUnsafeDescriptionSeparationNeedsClarification(t *testing.T) {
	cases := []struct {
		text, expression, amount, comment string
	}{
		{"айфон 5 5", "5", "5", "айфон 5 5"},
		{"кофе 200 или 300", "200 или 300", "300", "кофе 200 или 300"},
		{"кофе 350", "350", "300", "кофе 350"},
		{"кофе 350", "кофе 350", "350", "кофе 350"},
		{"кофе 350", "50", "50", "кофе 350"},
		{"кофе 350", "300", "300", "кофе 350"},
		{"такси за 450 рублей", "450", "450", "такси за 450 рублей"},
		{"такси за 450 рублей", "за 450 рублей", "450", "такси 450"},
		{"заказ #123", "123", "123", "заказ #123"},
		{"кофе -350", "350", "350", "кофе -350"},
		{"кофе 120,501", "120,501", "120.50", "кофе 120,501"},
		{"кофе 350", "", "350", "кофе 350"},
	}
	for _, test := range cases {
		t.Run(test.text+"/"+test.expression, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			args := map[string]string{"amount": test.amount, "spent_at": "today", "comment": test.comment}
			if test.expression != "" {
				args["amount_text"] = test.expression
			}
			encoded, err := json.Marshal(args)
			if err != nil {
				t.Fatal(err)
			}
			reply := handle(t, orchestrator, model, 1, 101, test.text, "create_expense", string(encoded))
			if reply != clarificationQuestions["request"] || len(store.repo.items) != 0 || orchestrator.states[101].pending.field != "request" {
				t.Fatal("unverified price fragment changed expenses")
			}
		})
	}
}

func TestOptionalDescriptionAndAmountFollowup(t *testing.T) {
	for _, comment := range []string{`null`, `""`, `"350"`} {
		orchestrator, model, store := setup(t)
		handle(t, orchestrator, model, 1, 101, "350", "create_expense", `{"amount":"350","amount_text":"350","spent_at":"today","comment":`+comment+`}`)
		if len(store.repo.items) != 1 || (store.repo.items[1].Comment != nil && *store.repo.items[1].Comment != "") {
			t.Fatal("missing description was invented or prevented creation")
		}
	}
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "айфон 5", "clarify", `{"field":"amount","intent":"create_expense","comment":"айфон 5"}`)
	handle(t, orchestrator, model, 2, 101, "6050", "answer_clarification", `{"value":"6050"}`)
	if store.repo.items[1].Comment == nil || *store.repo.items[1].Comment != "айфон 5" {
		t.Fatal("amount followup removed a model number")
	}
}

func TestDescriptionToolGuidance(t *testing.T) {
	orchestrator, _, _ := setup(t)
	request := orchestrator.request("synthetic", time.Now(), state{})
	if !strings.Contains(request.Messages[0].Content, `comment="айфон 5"`) {
		t.Fatal("missing product model example")
	}
	for _, definition := range request.Tools {
		if definition.Function.Name != "create_expense" && definition.Function.Name != "clarify" {
			continue
		}
		var schema struct {
			Properties map[string]struct{ Description string }
		}
		if err := json.Unmarshal(definition.Function.Parameters, &schema); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(schema.Properties["comment"].Description, "without the extracted price") || !strings.Contains(schema.Properties["amount_text"].Description, "exact price expression") {
			t.Fatal("description/price contract missing from schema")
		}
	}
}

func TestPriceFragmentPreservesCurrencyValidationAndCommentEdits(t *testing.T) {
	orchestrator, model, store := setup(t)
	reply := handle(t, orchestrator, model, 1, 101, "магазин 123 USD", "create_expense", `{"amount":"123","amount_text":"123","spent_at":"today","comment":"магазин 123 USD"}`)
	if len(store.repo.items) != 0 || !strings.Contains(reply, "рублях") {
		t.Fatal("price fragment bypassed currency validation")
	}
	handle(t, orchestrator, model, 2, 101, "айфон 5 6050", "create_expense", `{"amount":"6050","amount_text":"6050","spent_at":"today","comment":"айфон 5 6050"}`)
	handle(t, orchestrator, model, 3, 101, "комментарий: айфон 5 6050", "update_expense", `{"comment":"айфон 5 6050"}`)
	if store.repo.items[1].Comment == nil || *store.repo.items[1].Comment != "айфон 5 6050" {
		t.Fatal("explicit comment edit was rewritten")
	}
	handle(t, orchestrator, model, 4, 101, "удали последнюю", "delete_expense", `{}`)
	if len(store.repo.items) != 0 {
		t.Fatal("deletion regressed")
	}
}
