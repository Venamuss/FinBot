package conversation

import (
	"testing"
	"time"
)

func TestNaturalCorrectionPhrases(t *testing.T) {
	for _, test := range []struct{ original, description, text, amount string }{
		{"200", "водка", "последний был 400", "400"},
		{"200", "водка", "последний был 500", "500"},
		{"350", "кофе", "последнее было 450", "450"},
		{"350", "кофе", "последняя трата была 450", "450"},
		{"350", "кофе", "последний расход на самом деле 600", "600"},
		{"200", "водка", "не 200, а 400", "400"},
		{"350", "кофе", "не 350, а 450", "450"},
		{"350", "кофе", "исправь последнюю трату на 500", "500"},
		{"350", "кофе", "кофе был не 350, а 450", "450"},
		{"200", "водка", "паследний был 400", "400"},
	} {
		t.Run(test.text, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			handle(t, orchestrator, model, 1, 101, test.original+" "+test.description, "create_expense", `{"amount":"`+test.original+`","spent_at":"today","comment":"`+test.description+`"}`)
			reply := handle(t, orchestrator, model, 2, 101, test.text, "update_expense", `{"amount":"`+test.amount+`"}`)
			if store.repo.sequence != 1 || store.repo.items[1].Amount.String() != test.amount+".00" {
				t.Fatal("same expense was not updated:", reply)
			}
		})
	}
}

func TestNaturalCorrectionUsesSelectedFocus(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "кофе 200", "create_expense", `{"amount":"200","spent_at":"today","comment":"кофе"}`)
	handle(t, orchestrator, model, 2, 101, "такси 900", "create_expense", `{"amount":"900","spent_at":"today","comment":"такси"}`)
	handle(t, orchestrator, model, 3, 101, "покажи кофе", "list_expenses", `{"query":"кофе"}`)
	handle(t, orchestrator, model, 4, 101, "последний был 400", "update_expense", `{"amount":"400"}`)
	if store.repo.items[1].Amount.String() != "400.00" || store.repo.items[2].Amount.String() != "900.00" {
		t.Fatal("correction ignored selected focus in favor of latest expense")
	}
}

func TestNaturalCorrectionWithoutFocus(t *testing.T) {
	for _, withExpense := range []bool{false, true} {
		orchestrator, model, store := setup(t)
		if withExpense {
			handle(t, orchestrator, model, 1, 101, "кофе 200", "create_expense", `{"amount":"200","spent_at":"today"}`)
			delete(orchestrator.states, 101)
		}
		handle(t, orchestrator, model, 2, 101, "последний был 400", "update_expense", `{"amount":"400"}`)
		if withExpense {
			if store.repo.items[1].Amount.String() != "400.00" {
				t.Fatal("missing focus did not use the owner's latest created expense")
			}
		} else if len(store.repo.items) != 0 || orchestrator.states[101].pending != nil {
			t.Fatal("missing expense created a replacement or a deferred write")
		}
	}
}

func TestNaturalCorrectionWithStaleFocus(t *testing.T) {
	for _, mode := range []string{"updated", "deleted", "reassigned"} {
		t.Run(mode, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			handle(t, orchestrator, model, 1, 101, "такси 900", "create_expense", `{"amount":"900","spent_at":"today","comment":"такси"}`)
			handle(t, orchestrator, model, 2, 101, "кофе 200", "create_expense", `{"amount":"200","spent_at":"today","comment":"кофе"}`)
			item := store.repo.items[2]
			switch mode {
			case "updated":
				item.UpdatedAt = item.UpdatedAt.Add(time.Second)
				store.repo.items[2] = item
			case "deleted":
				delete(store.repo.items, 2)
			case "reassigned":
				item.UserID = 202
				store.repo.items[2] = item
			}
			writes := store.repo.expenseWrites
			reply := handle(t, orchestrator, model, 3, 101, "последний был 400", "update_expense", `{"amount":"400"}`)
			if reply != staleSelection || store.repo.expenseWrites != writes || store.repo.items[1].Amount.String() != "900.00" {
				t.Fatal("stale focus silently fell back to another expense:", reply)
			}
		})
	}
}

func TestNaturalCorrectionWithAmbiguousTarget(t *testing.T) {
	orchestrator, model, store := setup(t)
	for id := int64(1); id <= 2; id++ {
		handle(t, orchestrator, model, id, 101, "кофе 350", "create_expense", `{"amount":"350","spent_at":"today","comment":"кофе"}`)
	}
	handle(t, orchestrator, model, 3, 101, "кофе был не 350, а 450", "update_expense", `{"query":"кофе","amount":"450"}`)
	pending := orchestrator.states[101].pending
	if pending == nil || pending.field != "selection" || store.repo.items[1].Amount.String() != "350.00" || store.repo.items[2].Amount.String() != "350.00" {
		t.Fatal("ambiguous description changed an arbitrary expense")
	}
	handle(t, orchestrator, model, 4, 101, "лвлала", "choose_expense", `{"choice":"2"}`)
	if orchestrator.states[101].pending != pending {
		t.Fatal("unclear text resolved the selection")
	}
	handle(t, orchestrator, model, 5, 101, "2", "choose_expense", `{"choice":"2"}`)
	handle(t, orchestrator, model, 6, 101, "последний был 500", "update_expense", `{"amount":"500"}`)
	if store.repo.items[1].Amount.String() != "500.00" || store.repo.items[2].Amount.String() != "350.00" {
		t.Fatal("numeric selection or subsequent focus correction chose the wrong expense")
	}
}

func TestNaturalCorrectionReplacesPendingOperation(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "200 водка", "create_expense", `{"amount":"200","spent_at":"today","comment":"водка"}`)
	handle(t, orchestrator, model, 2, 101, "кофе", "clarify", `{"field":"amount","comment":"кофе"}`)
	handle(t, orchestrator, model, 3, 101, "последний был 400", "update_expense", `{"amount":"400","request_context":"followup"}`)
	if len(store.repo.items) != 1 || store.repo.items[1].Amount.String() != "400.00" || orchestrator.states[101].pending != nil {
		t.Fatal("independent correction was merged into an unrelated pending creation")
	}
}

func TestContextualFieldRepliesRemainSupported(t *testing.T) {
	for _, test := range []struct{ field, reply, value string }{
		{"category", "еду", "еда"},
		{"category", "другое", "другое"},
		{"spent_at", "2026-10-15", "2026-10-15"},
		{"spent_at", "15.10.2026", "2026-10-15"},
		{"spent_at", "четверг", "2026-10-15"},
	} {
		t.Run(test.reply, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			handle(t, orchestrator, model, 1, 101, "кофе 350", "create_expense", `{"amount":"350","spent_at":"today","category":"покупки"}`)
			text := "измени категорию"
			if test.field == "spent_at" {
				text = "измени дату"
			}
			handle(t, orchestrator, model, 2, 101, text, "clarify", `{"field":"`+test.field+`","intent":"update_expense"}`)
			handle(t, orchestrator, model, 3, 101, test.reply, "answer_clarification", `{"value":"`+test.value+`"}`)
			if orchestrator.states[101].pending != nil || store.repo.items[1].Amount.String() != "350.00" {
				t.Fatal("valid field reply was rejected or changed the amount")
			}
			item := store.repo.items[1]
			if test.field == "category" && string(item.Category) != test.value {
				t.Fatal("category reply did not update the requested field")
			}
			if test.field == "spent_at" && string(item.SpentAt) != test.value {
				t.Fatal("date reply did not update the requested field")
			}
		})
	}
}
