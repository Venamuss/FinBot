package conversation

import (
	"strings"
	"unicode"
)

// These guards validate currency only. Intent, amounts, dates and quantities
// remain the model's responsibility; no expense command parser runs here.
func unsupportedCurrency(text string) bool {
	for _, character := range text {
		if unicode.Is(unicode.Sc, character) && character != '₽' {
			return true
		}
	}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), nonLetter) {
		switch word {
		case "usd", "eur", "gbp", "cny", "jpy", "chf", "cad", "aud", "byn", "kzt", "uah", "try", "inr", "aed", "usdt", "btc", "eth",
			"доллар", "доллара", "долларов", "доллары", "долларах", "евро",
			"юань", "юаня", "юаней", "юани", "юанях", "иен", "иена", "иены", "йен", "йена", "йены",
			"фунт", "фунта", "фунтов", "фунты", "фунтах", "гривна", "гривны", "гривен", "тенге",
			"дирхам", "дирхама", "дирхамов", "лира", "лиры", "лир", "рупия", "рупии", "рупий",
			"франк", "франка", "франков", "биткоин", "биткоина", "биткоинов":
			return true
		}
	}
	return false
}

func explicitRubles(text string) bool {
	if strings.ContainsRune(text, '₽') {
		return true
	}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), nonLetter) {
		switch word {
		case "rub", "rur", "р", "руб", "рубль", "рубля", "рублей", "рубли", "рублях":
			return true
		}
	}
	return false
}

func nonLetter(character rune) bool {
	return !unicode.IsLetter(character)
}

func currencyClarification(operation action) action {
	result := copyAction(operation)
	field := "currency"
	result.args["field"] = &field
	if operation.name == "create_expense" || operation.name == "update_expense" {
		intent := operation.name
		result.args["intent"] = &intent
	}
	result.name = "clarify"
	return result
}
