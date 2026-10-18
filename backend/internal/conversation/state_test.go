package conversation

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWrongSelectionToolCannotCancelCurrencyCorrection(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "магазин 123$", "create_expense", `{"amount":"123","category":"покупки","spent_at":"today","comment":"магазин"}`)
	reply := handle(t, orchestrator, model, 2, 101, "13", "choose_expense", `{"choice":"cancel"}`)
	if !strings.Contains(reply, "рублях") || orchestrator.states[101].pending == nil || len(store.repo.items) != 0 {
		t.Fatal("wrong selection tool canceled a currency correction")
	}
}

func TestCreationCategoryClarificationUsesFallback(t *testing.T) {
	orchestrator, model, store := setup(t)
	reply := handle(t, orchestrator, model, 1, 101, "неведомая вещь 120", "clarify", `{"field":"category","intent":"create_expense","amount":"120","spent_at":"today","comment":"неведомая вещь"}`)
	if len(store.repo.items) != 1 || !strings.Contains(reply, "120 ₽") || store.repo.items[1].Category != "другое" {
		t.Fatal("creation requested a category despite a valid fallback")
	}
}

func TestReportedSequenceCannotCrossStateTypes(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "магазин 123$", "create_expense", `{"amount":"123","category":"покупки","spent_at":"today","comment":"магазин"}`)
	for index, test := range []struct{ text, name, args string }{
		{"13", "choose_expense", `{"choice":"cancel"}`},
		{"13", "cancel_request", `{}`},
		{"13", "answer_clarification", `{"value":"RUB"}`},
	} {
		reply := handle(t, orchestrator, model, int64(index+2), 101, test.text, test.name, test.args)
		if !strings.Contains(reply, "рублях") || orchestrator.states[101].stateType() != "awaiting_currency" || len(store.repo.items) != 0 {
			t.Fatal("numeric currency reply changed state or mutated expenses")
		}
	}
	// Even a spurious category clarification cannot discard the valid draft.
	reply := handle(t, orchestrator, model, 5, 101, "123р", "clarify", `{"field":"category"}`)
	if len(store.repo.items) != 1 || store.repo.items[1].Amount.String() != "123.00" || store.repo.items[1].Comment == nil || *store.repo.items[1].Comment != "магазин" {
		t.Fatalf("RUB correction lost the original draft: %s", reply)
	}
	handle(t, orchestrator, model, 6, 101, "другое", "choose_expense", `{"choice":"1"}`)
	if len(store.repo.items) != 1 || orchestrator.states[101].pending != nil {
		t.Fatal("idle category word created a record selection")
	}
	handle(t, orchestrator, model, 7, 101, "другое 120", "create_expense", `{"amount":"120","category":"другое","spent_at":"today","comment":"другое"}`)
	handle(t, orchestrator, model, 8, 101, "неведомая штуковина 500", "create_expense", `{"amount":"500","spent_at":"today","comment":"неведомая штуковина"}`)
	if len(store.repo.items) != 3 || store.repo.items[2].Category != "другое" || store.repo.items[3].Category != "другое" {
		t.Fatal("explicit or fallback category failed")
	}
}

func TestAmountDraftAnswerPreservesDataAndDate(t *testing.T) {
	orchestrator, model, store := setup(t)
	original := time.Date(2026, 10, 16, 20, 30, 0, 0, time.UTC)
	model.response = tool("clarify", `{"field":"amount","intent":"create_expense","category":"еда","spent_at":"yesterday","comment":"кофе"}`)
	if _, err := orchestrator.Handle(context.Background(), 1, 101, "кофе вчера", original); err != nil {
		t.Fatal(err)
	}
	for index, test := range []struct{ name, args string }{
		{"clarify", `{"field":"category"}`},
		{"choose_expense", `{"choice":"1"}`},
		{"update_expense", `{"id":"1","amount":"350"}`},
	} {
		reply := handle(t, orchestrator, model, int64(index+2), 101, "350", test.name, test.args)
		if reply != "Какую сумму записать?" || len(store.repo.items) != 0 {
			t.Fatal("wrong tool changed the expected amount field")
		}
	}
	model.response = tool("answer_clarification", `{"value":"350.25"}`)
	reply, err := orchestrator.Handle(context.Background(), 5, 101, "350,25", original.Add(2*time.Hour))
	if err != nil || len(store.repo.items) != 1 {
		t.Fatalf("amount answer failed: %v", err)
	}
	item := store.repo.items[1]
	if item.Amount.String() != "350.25" || item.Category != "еда" || item.SpentAt != "2026-10-15" || item.Comment == nil || *item.Comment != "кофе" {
		t.Fatal("amount answer overwrote already valid information")
	}
	calls := model.calls
	replayed, err := orchestrator.Handle(context.Background(), 5, 101, "350,25", original.Add(2*time.Hour))
	if err != nil || replayed != reply || model.calls != calls || store.repo.sequence != 1 {
		t.Fatal("clarification reply created a duplicate")
	}
}

func TestCategoryAnswerUpdatesOnlyTheRequestedExpense(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "кофе 50", "create_expense", `{"amount":"50","category":"еда","spent_at":"today","comment":"кофе"}`)
	handle(t, orchestrator, model, 2, 202, "такси 120", "create_expense", `{"amount":"120","category":"транспорт","spent_at":"today"}`)
	handle(t, orchestrator, model, 3, 101, "измени категорию последней траты", "clarify", `{"field":"category","intent":"update_expense","id":"1"}`)
	handle(t, orchestrator, model, 4, 202, "кофе", "clarify", `{"field":"amount","intent":"create_expense","category":"еда","comment":"кофе"}`)
	if orchestrator.states[101].stateType() != "awaiting_category" || orchestrator.states[202].stateType() != "awaiting_amount" {
		t.Fatal("user clarification states are not independent")
	}
	handle(t, orchestrator, model, 5, 101, "другое", "choose_expense", `{"choice":"1"}`)
	if store.repo.items[1].Category != "еда" {
		t.Fatal("category answer was mistaken for an expense selection")
	}
	handle(t, orchestrator, model, 6, 101, "другое", "answer_clarification", `{"value":"другое"}`)
	if len(store.repo.items) != 2 || store.repo.items[1].Category != "другое" || store.repo.items[1].Amount.String() != "50.00" || store.repo.items[2].Category != "транспорт" {
		t.Fatal("category answer changed another field or owner")
	}
	if orchestrator.states[202].stateType() != "awaiting_amount" {
		t.Fatal("answer cleared another user's pending amount")
	}
	handle(t, orchestrator, model, 7, 202, "350", "answer_clarification", `{"value":"350"}`)
	if len(store.repo.items) != 3 || store.repo.items[3].UserID != 202 {
		t.Fatal("second user's amount answer failed")
	}
	reads, calls := store.reads, model.calls
	reply := handle(t, orchestrator, model, 8, 303, "другое", "answer_clarification", `{"value":"другое"}`)
	if reply != Denied || store.reads != reads || model.calls != calls {
		t.Fatal("denied user accessed clarification state or data")
	}
}

func TestSelectionCancellationAndNewCreation(t *testing.T) {
	orchestrator, model, store := setup(t)
	for id := int64(1); id <= 2; id++ {
		handle(t, orchestrator, model, id, 101, "такси 350", "create_expense", `{"amount":"350","category":"транспорт","spent_at":"today","comment":"такси"}`)
	}
	handle(t, orchestrator, model, 3, 101, "удали такси", "delete_expense", `{"query":"такси"}`)
	if orchestrator.states[101].stateType() != "selection" {
		t.Fatal("ambiguous delete did not enter selection")
	}
	handle(t, orchestrator, model, 4, 101, "13", "choose_expense", `{"choice":"cancel"}`)
	if orchestrator.states[101].pending == nil || len(store.repo.items) != 2 {
		t.Fatal("arbitrary number canceled the pending selection")
	}
	handle(t, orchestrator, model, 5, 101, "отмена", "cancel_request", `{}`)
	if orchestrator.states[101].pending != nil || len(store.repo.items) != 2 {
		t.Fatal("explicit cancellation mutated expenses or retained selection")
	}
	handle(t, orchestrator, model, 6, 101, "удали такси", "delete_expense", `{"query":"такси"}`)
	handle(t, orchestrator, model, 7, 101, "еда 50", "create_expense", `{"amount":"50","category":"еда","spent_at":"today","comment":"еда"}`)
	handle(t, orchestrator, model, 8, 101, "первая", "choose_expense", `{"choice":"1"}`)
	if len(store.repo.items) != 3 || orchestrator.states[101].pending != nil {
		t.Fatal("superseded selection mutated data")
	}
}

func TestClarificationExpiryAndIndependentFailure(t *testing.T) {
	orchestrator, model, store := setup(t)
	now := time.Now()
	orchestrator.now = func() time.Time {
		return now
	}
	handle(t, orchestrator, model, 1, 101, "кофе", "clarify", `{"field":"amount","comment":"кофе","category":"еда"}`)
	now = now.Add(30 * time.Minute)
	handle(t, orchestrator, model, 2, 101, "350", "answer_clarification", `{"value":"350"}`)
	if len(store.repo.items) != 0 || orchestrator.states[101].pending != nil || strings.Contains(model.request.Messages[0].Content, `"pending_request"`) {
		t.Fatal("expired draft was restored or executed")
	}
	handle(t, orchestrator, model, 3, 101, "магазин 123$", "create_expense", `{"amount":"123","category":"покупки","spent_at":"today"}`)
	handle(t, orchestrator, model, 4, 101, "неверная покупка", "create_expense", `{"amount":"1.001","spent_at":"today"}`)
	if len(store.repo.items) != 0 || orchestrator.states[101].pending != nil {
		t.Fatal("failed independent command kept an unrelated currency draft")
	}
	handle(t, orchestrator, model, 5, 101, "другое 120", "create_expense", `{"amount":"120","category":"другое","spent_at":"today"}`)
	if len(store.repo.items) != 1 {
		t.Fatal("fresh command after failure did not recover")
	}
}

func TestMalformedAnswerPreservesTheValidatedDraft(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "кофе", "clarify", `{"field":"amount","category":"еда","comment":"кофе"}`)
	original := orchestrator.states[101].pending
	handle(t, orchestrator, model, 2, 101, "1,001", "answer_clarification", `{"value":"1.001"}`)
	if len(store.repo.items) != 0 || orchestrator.states[101].pending != original || original.action.value("amount") != "" {
		t.Fatal("invalid answer mutated the committed draft")
	}
	handle(t, orchestrator, model, 3, 101, "350", "answer_clarification", `{"value":"350"}`)
	if len(store.repo.items) != 1 || store.repo.items[1].Category != "еда" {
		t.Fatal("valid answer after malformed amount failed")
	}
}

func TestNewCreationSupersedesAmountClarification(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "кофе", "clarify", `{"field":"amount","category":"еда","comment":"кофе"}`)
	handle(t, orchestrator, model, 2, 101, "другое 120", "clarify", `{"field":"category","intent":"create_expense","amount":"120","spent_at":"today","comment":"другое","request_context":"new"}`)
	if len(store.repo.items) != 1 || store.repo.items[1].Category != "другое" || store.repo.items[1].Comment == nil || *store.repo.items[1].Comment != "другое" || orchestrator.states[101].pending != nil {
		t.Fatal("new creation inherited the previous missing-amount draft")
	}
}

func TestCategoryReplyCannotRetargetAnUpdate(t *testing.T) {
	orchestrator, model, store := setup(t)
	for id := int64(1); id <= 2; id++ {
		handle(t, orchestrator, model, id, 101, "кофе 50", "create_expense", `{"amount":"50","category":"еда","spent_at":"today"}`)
	}
	handle(t, orchestrator, model, 3, 101, "измени категорию последней", "clarify", `{"field":"category","intent":"update_expense"}`)
	handle(t, orchestrator, model, 4, 101, "другое", "update_expense", `{"category":"другое","id":"1","amount":"999","request_context":"followup"}`)
	if store.repo.items[1].Category != "еда" || store.repo.items[2].Category != "другое" || store.repo.items[2].Amount.String() != "50.00" {
		t.Fatal("category-only reply injected a different target or amount")
	}
}

func TestExplicitCurrencyCorrectionReplacesUnsupportedDraftCode(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "магазин 123 USD", "create_expense", `{"amount":"123","category":"покупки","spent_at":"today","currency":"USD","comment":"магазин"}`)
	handle(t, orchestrator, model, 2, 101, "123 руб", "create_expense", `{"amount":"123","category":"покупки","spent_at":"today","request_context":"followup"}`)
	if len(store.repo.items) != 1 || store.repo.items[1].Amount.String() != "123.00" {
		t.Fatal("explicit RUB correction retained the rejected currency code")
	}
}
