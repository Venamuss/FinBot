package conversation

import (
	"context"
	"strings"
	"time"

	"github.com/Venamuss/FinBot/internal/botresponse"
	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/internal/llm"
	"github.com/Venamuss/FinBot/internal/observability"
)

var clarificationQuestions = map[string]string{
	"amount":   "Какую сумму записать?",
	"currency": botresponse.UnsupportedCurrency,
	"category": "Уточните категорию траты.",
	"spent_at": "Уточните дату траты.",
	"target":   "Какую трату выбрать?",
	"request":  "Уточните действие и данные траты.",
}

func (current *pending) selecting() bool {
	return current != nil && contains([]string{"selection", "confirmation", "delete_confirmation"}, current.field)
}

func (current state) stateType() string {
	if current.pending == nil {
		return "idle"
	}
	if current.pending.selecting() {
		return current.pending.field
	}
	if _, known := clarificationQuestions[current.pending.field]; known {
		return "awaiting_" + current.pending.field
	}

	return "awaiting_request"
}

func pendingQuestion(current *pending) string {
	if current == nil {
		return "Нет ожидающего уточнения. Укажите действие и данные траты."
	}
	if current.selecting() {
		if current.field == "delete_confirmation" {
			return "Подтвердите удаление: ответьте да или нет."
		}
		return "Сначала завершите выбор: ответьте номером предложенной траты или отмените действие."
	}

	return clarificationQuestions[current.field]
}

func toolsForState(current state) []llm.Tool {
	definitions := toolsForModel()
	available := make([]llm.Tool, 0, len(definitions))
	for _, definition := range definitions {
		switch definition.Function.Name {
		case "choose_expense":
			if !current.pending.selecting() || current.pending.field == "delete_confirmation" {
				continue
			}
		case "answer_clarification":
			if current.pending == nil || current.pending.action.name == "" || !contains([]string{"amount", "category", "spent_at", "currency"}, current.pending.field) {
				continue
			}
		case "cancel_request":
			if current.pending == nil {
				continue
			}
		}
		available = append(available, definition)
	}

	return available
}

func explicitCancellation(text string) bool {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(text), ".!")) {
	case "отмена", "отмени", "отменить", "отмени действие", "отменить действие", "не надо", "cancel", "стоп":
		return true
	default:
		return false
	}
}

func cancelPending(ctx context.Context, text string, next *state) string {
	if !explicitCancellation(text) {
		return rejectStateTool(ctx, next, "cancel_request")
	}
	if next.pending == nil {
		return "Нет активной операции для отмены."
	}
	next.pending = nil
	return "Выбор отменён."
}

func rejectStateTool(ctx context.Context, next *state, tool string) string {
	observability.Logger(ctx, "conversation", "route_action").WarnContext(ctx, "Conversation action rejected",
		"tool", tool, "state_type", next.stateType(), "error_category", "state_mismatch")
	return pendingQuestion(next.pending)
}

func copyAction(operation action) action {
	result := action{name: operation.name, args: make(map[string]*string, len(operation.args))}
	for field, value := range operation.args {
		result.args[field] = value
	}

	return result
}

// Keep known draft values across retries and follow-ups without mutating the
// committed state's argument map. Only the requested field may be replaced.
func mergeDraft(original action, answer action, field string) action {
	merged := copyAction(answer)
	if original.name != "" {
		merged.name = original.name
	}
	for key, value := range original.args {
		_, supplied := answer.args[key]
		if !supplied || (key != field && !(field == "currency" && key == "amount")) {
			merged.args[key] = value
		}
	}

	return merged
}

func (orchestrator *Orchestrator) validateDraft(operation action, reference time.Time) error {
	for _, field := range []string{"amount", "previous_amount"} {
		if operation.args[field] != nil {
			if _, err := expense.ParseAmount(operation.value(field)); err != nil {
				return expense.ValidationError{"amount": "invalid"}
			}
		}
	}

	if operation.args["category"] != nil {
		if _, err := expense.ParseCategory(operation.value("category")); err != nil {
			return expense.ValidationError{"category": "invalid"}
		}
	}

	if operation.args["spent_at"] != nil {
		if _, err := orchestrator.date(operation.value("spent_at"), reference); err != nil {
			return expense.ValidationError{"spent_at": "invalid"}
		}
	}

	if operation.args["id"] != nil {
		if _, err := expense.ParseID(operation.value("id")); err != nil {
			return err
		}
	}

	if operation.args["comment"] != nil && strings.ContainsRune(operation.value("comment"), 0) {
		return expense.ValidationError{"comment": "invalid"}
	}

	return nil
}

func (orchestrator *Orchestrator) clarify(ctx context.Context, operation action, text string, reference time.Time, next *state) (action, time.Time, string, error) {
	field := operation.value("field")
	draft := copyAction(operation)
	draft.name = operation.value("intent")
	if draft.name == "" && field != "request" && field != "target" {
		draft.name = "create_expense"
	}
	for _, metadata := range []string{"field", "intent", "request_context"} {
		delete(draft.args, metadata)
	}

	if next.pending != nil {
		previous := next.pending
		if previous.action.name != "" && draft.name != "" && previous.action.name != draft.name {
			return action{}, reference, rejectStateTool(ctx, next, "clarify"), nil
		}
		draft = mergeDraft(previous.action, draft, previous.field)
		reference = previous.reference
		if previous.field != field && previous.field != "request" && previous.field != "target" {
			resolved := operation.args[previous.field] != nil && draft.value(previous.field) != ""
			if previous.field == "currency" {
				resolved = explicitRubles(text)
				if resolved {
					currency := "RUB"
					draft.args["currency"] = &currency
				}
			}
			if !resolved {
				return action{}, reference, rejectStateTool(ctx, next, "clarify"), nil
			}
		}
		text = previous.text + "\n" + text
	}

	if err := orchestrator.validateDraft(draft, reference); err != nil {
		if field == "currency" && unsupportedCurrency(text) {
			// Invalid model arguments must not hide source currency evidence or
			// leave an invalid draft available for a later clarification.
			next.pending = nil
			return action{}, reference, botresponse.UnsupportedCurrency, nil
		}

		return action{}, reference, "", err
	}

	if field == "category" && draft.name == "create_expense" {
		if draft.value("amount") != "" {
			return draft, reference, "", nil
		}
		// A creation has a category fallback; only its missing amount needs asking.
		field = "amount"
	}

	if len(text) > 16384 {
		next.pending = nil
		return action{}, reference, "Уточните запрос одним сообщением.", nil
	}
	next.pending = &pending{field: field, action: draft, text: text, reference: reference}
	return action{}, reference, clarificationQuestions[field], nil
}

func (orchestrator *Orchestrator) answer(ctx context.Context, operation action, text string, reference time.Time, next *state) (action, time.Time, string, error) {
	current := next.pending
	if current == nil || current.selecting() || current.action.name == "" {
		return action{}, reference, rejectStateTool(ctx, next, "answer_clarification"), nil
	}
	if operation.args["amount"] != nil && current.field != "currency" {
		return action{}, reference, rejectStateTool(ctx, next, "answer_clarification"), nil
	}
	if current.field == "currency" && (operation.value("value") != "RUB" || !explicitRubles(text) || unsupportedCurrency(text)) {
		return action{}, reference, rejectStateTool(ctx, next, "answer_clarification"), nil
	}
	if !contains([]string{"amount", "category", "spent_at", "currency"}, current.field) {
		return action{}, reference, rejectStateTool(ctx, next, "answer_clarification"), nil
	}
	draft := copyAction(current.action)
	if current.field == "amount" {
		amount, err := expense.ParseAmount(operation.value("value"))
		if err == nil && !containsPrice(text, amount) {
			return action{}, reference, rejectStateTool(ctx, next, "answer_clarification"), nil
		}
	}

	draft.args[current.field] = operation.args["value"]
	if operation.args["amount"] != nil {
		draft.args["amount"] = operation.args["amount"]
	}

	if err := orchestrator.validateDraft(draft, current.reference); err != nil {
		return action{}, reference, "", err
	}

	if draft.name == "create_expense" && draft.value("amount") == "" {
		field := "amount"
		draft.args["field"] = &field
		intent := draft.name
		draft.args["intent"] = &intent
		return orchestrator.clarify(ctx, draft, text, current.reference, next)
	}

	return draft, current.reference, "", nil
}
