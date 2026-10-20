package conversation

import (
	"strings"
	"unicode"

	"github.com/Venamuss/FinBot/internal/expense"
)

const unknownIntentReply = "Не понял запрос. Попробуйте переформулировать. Я могу добавить расход, показать траты или изменить существующую запись."

func unclearIntent(current *pending) string {
	if current != nil {
		return unknownIntentReply + "\n" + pendingQuestion(current)
	}
	return unknownIntentReply
}

// A proposed read needs evidence in this message, not just a model-selected
// tool or an existing focus/draft. These cues are deliberately local to reads:
// they are not a vocabulary restriction on expense descriptions or field replies.
// This is a conservative boundary, not a complete Russian intent classifier.
func readActionSupported(operation action, text string, current state) bool {
	switch operation.name {
	case "navigate_expenses":
		words := intentWords(text)
		for _, word := range words {
			if operation.value("direction") == "next" && contains([]string{"ещё", "еще", "дальше", "следующая", "следующую", "next"}, word) {
				return true
			}
			if operation.value("direction") == "previous" && contains([]string{"назад", "предыдущая", "предыдущую", "previous"}, word) {
				return true
			}
		}
		return false
	case "list_expenses", "total_expenses":
		if contains([]string{"update_expense", "delete_expense"}, messageAction(text)) {
			return false
		}
		for _, word := range intentWords(text) {
			if contains([]string{"покажи", "покажите", "показать", "глянь", "глянуть", "сколько", "скока", "список", "итого", "сводка", "list", "show", "total", "expenses", "spent", "spending"}, word) {
				return true
			}
			// Inflections and common spelling errors retain an expense cue even
			// when the request verb is informal or misspelled.
			for _, stem := range []string{"расход", "расхд", "расхдо", "трат", "потрат", "потрач"} {
				if strings.HasPrefix(word, stem) {
					return true
				}
			}
		}
		lower := strings.Join(intentWords(text), " ")
		return current.listing != nil && (strings.HasPrefix(lower, "из них ") || strings.HasPrefix(lower, "только "))
	default:
		return true
	}
}

func intentWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsNumber(character)
	})
}

// Check replies before merging drafts or clearing pending work. A model must
// not complete a valid draft using an invented value after a meaningless reply.
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
	words := intentWords(text)
	switch field {
	case "amount":
		amount, err := expense.ParseAmount(value)
		return err != nil || containsPrice(text, amount)
	case "category":
		// Include ordinary inflections/synonyms; this does not restrict descriptions.
		for _, word := range words {
			for _, stem := range []string{"ед", "питан", "продукт", "транспорт", "жиль", "жилищ", "развлеч", "здоров", "медицин", "покуп", "друг", "проч"} {
				if strings.HasPrefix(word, stem) {
					return true
				}
			}
		}
		return false
	case "spent_at":
		for _, word := range words {
			for _, character := range word {
				if unicode.IsDigit(character) {
					return true
				}
			}
			if contains([]string{"сегодня", "вчера", "позавчера", "завтра", "today", "yesterday"}, word) {
				return true
			}
			for _, stem := range []string{"понедельник", "вторник", "сред", "четверг", "пятниц", "суббот", "воскресен", "январ", "феврал", "март", "апрел", "май", "мая", "июн", "июл", "август", "сентябр", "октябр", "ноябр", "декабр"} {
				if strings.HasPrefix(word, stem) {
					return true
				}
			}
		}
		return false
	default:
		return true
	}
}
