package conversation

import (
	"context"
	"regexp"
	"strings"
	"unicode"

	"github.com/Venamuss/FinBot/internal/botresponse"
	"github.com/Venamuss/FinBot/internal/expense"
)

func controlText(text string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(text), ".!"))
}

// Only standalone control replies bypass planning. Words inside descriptions
// remain data. The model cannot manufacture a deletion confirmation.
func controlAction(text string, current state) (action, bool) {
	if explicitCancellation(text) {
		return action{name: "cancel_request"}, true
	}
	switch controlText(text) {
	case "нет", "no":
		if current.pending != nil {
			return action{name: "decline"}, true
		}
		return action{name: "acknowledge"}, true
	case "да", "yes", "подтверждаю":
		if current.pending != nil && current.pending.field == "delete_confirmation" {
			return action{name: "confirm_delete"}, true
		}
		if current.pending != nil && current.pending.field == "confirmation" {
			choice := "1"
			return action{name: "choose_expense", args: map[string]*string{"choice": &choice}}, true
		}
		return action{name: "acknowledge"}, true
	}
	return action{}, false
}

func explicitChoice(text string, confirmation bool) int64 {
	text = controlText(text)
	if confirmation && contains([]string{"да", "yes", "подтверждаю"}, text) {
		return 1
	}
	switch text {
	case "первая", "первый", "первое":
		return 1
	case "вторая", "второй", "второе":
		return 2
	}
	for _, prefix := range []string{"вариант ", "номер "} {
		text = strings.TrimPrefix(text, prefix)
	}
	choice, err := expense.ParseID(text)
	if err != nil {
		return 0
	}
	return choice
}

// This is a conservative boundary for explicit command families, not a natural
// language parser. General extraction and target resolution remain with the
// model. Unrecognized edit phrasing asks the user to restate the request.
func messageAction(text string) string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	if len(words) == 0 {
		return ""
	}
	if len(words) > 1 && contains([]string{"пожалуйста", "можешь", "можете"}, words[0]) {
		words = words[1:]
	}
	first := words[0]
	if contains([]string{"покажи", "показать", "сколько", "список", "мои", "расходы", "траты", "ещё", "еще", "дальше", "назад", "следующая", "предыдущая", "из", "list", "show", "total", "next", "previous"}, first) {
		return "read"
	}
	if contains([]string{"удали", "удалить", "убери", "убрать", "сотри", "delete", "remove"}, first) {
		return "delete_expense"
	}
	if contains([]string{"измени", "изменить", "поменяй", "исправь", "скорректируй", "обнови", "очисти", "комментарий", "категория", "сумма", "дата", "последнее", "update", "edit", "change"}, first) {
		return "update_expense"
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "было") || strings.Contains(lower, "не ") && strings.Contains(lower, ", а ") {
		return "update_expense"
	}
	return ""
}

func actionMatchesMessage(operation action, text string, current state) bool {
	if operation.name == "clarify" && operation.value("intent") != "" {
		draft := copyAction(operation)
		draft.name = operation.value("intent")
		return actionMatchesMessage(draft, text, current)
	}
	expected := messageAction(text)
	mutation := contains([]string{"create_expense", "update_expense", "delete_expense", "answer_clarification", "choose_expense"}, operation.name)
	if expected == "read" && mutation {
		return false
	}
	if contains([]string{"update_expense", "delete_expense"}, expected) && mutation && operation.name != expected {
		return false
	}
	if operation.name != "update_expense" && operation.name != "delete_expense" {
		return true
	}
	if expected == operation.name {
		return true
	}
	return current.pending != nil && current.pending.action.name == operation.name &&
		!current.pending.selecting() && operation.value("request_context") != "new" && (operation.value("request_context") == "followup" ||
		contains([]string{"amount", "category", "spent_at", "currency"}, current.pending.field))
}

var explicitExpenseID = regexp.MustCompile(`#([1-9][0-9]*)\b`)
var explicitCorrection = regexp.MustCompile(`(?i)(?:^|\s)не\s+` + pricePattern + `\s*,?\s*а\s+` + pricePattern)

func sourceCorrection(operation action, text string) (action, error) {
	corrections := explicitCorrection.FindAllStringSubmatch(text, -1)
	if len(corrections) == 0 {
		return operation, nil
	}
	if len(corrections) != 1 {
		return action{}, errArguments
	}
	previous, err := expense.ParseAmount(strings.ReplaceAll(corrections[0][1], ",", "."))
	if err != nil {
		return action{}, errArguments
	}
	amount, err := expense.ParseAmount(strings.ReplaceAll(corrections[0][2], ",", "."))
	if err != nil {
		return action{}, errArguments
	}
	proposed, err := expense.ParseAmount(operation.value("amount"))
	if err != nil || proposed != amount {
		return action{}, errArguments
	}
	if value := operation.value("previous_amount"); value != "" {
		proposedPrevious, err := expense.ParseAmount(value)
		if err != nil || proposedPrevious != previous {
			return action{}, errArguments
		}
	}
	result := copyAction(operation)
	value := previous.String()
	result.args["previous_amount"] = &value
	return result, nil
}

func matchesExplicitID(operation action, text string) bool {
	matches := explicitExpenseID.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return true
	}
	return len(matches) == 1 && operation.value("id") == matches[0][1] && operation.value("query") == ""
}

func (orchestrator *Orchestrator) confirmDelete(ctx context.Context, service *expense.OwnerService, owner int64, text string, next *state) (string, error) {
	deferred := next.pending
	if deferred == nil || deferred.field != "delete_confirmation" || deferred.action.name != "delete_expense" ||
		len(deferred.candidates) != 1 || !contains([]string{"да", "yes", "подтверждаю"}, controlText(text)) {
		return rejectStateTool(ctx, next, "confirm_delete"), nil
	}
	selected := deferred.candidates[0]
	// No new lookup or fallback: confirmation is bound to exactly this owner,
	// expense and displayed version. SQL checks all three atomically.
	if err := service.Delete(ctx, owner, selected.ID, selected.UpdatedAt); err != nil {
		next.pending = nil
		next.focus = nil
		return operationError(ctx, err)
	}
	next.pending = nil
	if next.focus != nil && next.focus.ID == selected.ID {
		next.focus = nil
	}
	return botresponse.Deleted(selected), nil
}

func requestedUpdateFields(operation action, source string) bool {
	lower := strings.ToLower(source)
	if value := operation.value("amount"); value != "" {
		amount, err := expense.ParseAmount(value)
		if err != nil || !containsPrice(source, amount) {
			return false
		}
	}
	if value := operation.value("category"); value != "" && !strings.Contains(lower, "категор") && !strings.Contains(lower, value) {
		return false
	}
	if value := operation.value("spent_at"); value != "" && !strings.Contains(lower, "дат") &&
		!strings.Contains(lower, "сегодня") && !strings.Contains(lower, "вчера") && !strings.Contains(lower, value) {
		return false
	}
	if operation.args["comment"] != nil && !strings.Contains(lower, "комментар") && !strings.Contains(lower, "описан") && !strings.Contains(lower, "назван") {
		return false
	}
	return true
}
