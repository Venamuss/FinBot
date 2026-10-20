package conversation

import (
	"strings"
	"unicode"

	"github.com/Venamuss/FinBot/internal/expense"
)

const unknownIntentReply = "Не понял запрос. Попробуйте переформулировать."

func unclearIntent(current *pending) string {
	if current != nil {
		return unknownIntentReply + "\n" + pendingQuestion(current)
	}

	return unknownIntentReply
}

// This guard does not classify arbitrary Russian sentences. It prevents a lone
// opaque token (or punctuation) from authorizing a read just because the model
// chose a read tool. Multiword interpretation remains with the configured model;
// no general verb/category vocabulary is imposed on natural-language requests.
func readActionSupported(operation action, text string, current state) bool {
	if !contains([]string{"list_expenses", "total_expenses", "navigate_expenses"}, operation.name) {
		return true
	}

	expected := messageAction(text)
	if expected == "update_expense" || expected == "delete_expense" {
		return false
	}

	words := intentWords(text)
	if len(words) == 0 {
		return false
	}
	if len(words) > 1 && len(strings.Fields(text)) > 1 {
		return true
	}

	if expected == "read" && !contains([]string{"мои", "из"}, words[0]) {
		return true
	}

	// A category alone may refine a previous listing, but an arbitrary token
	// must not become a new listing merely because there is conversation focus.
	if len(words) != 1 {
		return false
	}
	category, err := expense.ParseCategory(words[0])
	return current.listing != nil && err == nil && operation.value("category") == string(category)
}

func intentWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsNumber(character)
	})
}

// Validate expected field replies before merging a draft. This is deliberately
// confined to pending work; independent corrections use the existing source
// amount, target, ownership and version checks rather than a phrase whitelist.
func pendingReplySupported(operation action, text string, current state) bool {
	if current.pending == nil || current.pending.selecting() || operation.value("request_context") == "new" {
		return true
	}

	continuation := operation.name == "answer_clarification" || operation.value("request_context") == "followup" ||
		(operation.name == "update_expense" && messageAction(text) != "update_expense") || operation.name == "clarify"
	if !continuation {
		return true
	}

	field := current.pending.field
	value := operation.value(field)
	if operation.name == "answer_clarification" {
		value = operation.value("value")
	}
	if value == "" {
		return true
	}

	switch field {
	case "amount":
		amount, err := expense.ParseAmount(value)
		if err != nil {
			// Leave malformed values to the existing amount validator.
			return true
		}

		return containsPrice(text, amount)
	case "category", "spent_at":
		// Restrict only opaque one-word answers. Natural multiword answers,
		// human-readable dates and numeric dates keep their existing parsing.
		words := intentWords(text)
		if len(words) > 1 && len(strings.Fields(text)) > 1 {
			return true
		}
		if field == "spent_at" {
			for _, character := range text {
				if unicode.IsDigit(character) {
					return true
				}
			}
		}
		if len(words) != 1 {
			return false
		}
		if field == "category" {
			// The closed category field can use Russian case endings, unlike
			// descriptions, whose vocabulary remains unrestricted.
			stem := map[string]string{
				"еда": "ед", "транспорт": "транспорт", "жильё": "жил",
				"развлечения": "развлеч", "здоровье": "здоров",
				"покупки": "покуп", "другое": "друг",
			}[value]
			return stem != "" && strings.HasPrefix(words[0], stem)
		}

		return contains([]string{"сегодня", "вчера", "позавчера", "завтра", "today", "yesterday", "понедельник", "вторник", "среда", "среду", "четверг", "пятница", "пятницу", "суббота", "субботу", "воскресенье"}, words[0])
	default:
		return true
	}
}
