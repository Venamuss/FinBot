package conversation

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Venamuss/FinBot/internal/expense"
)

var errDescription = errors.New("expense description needs clarification")

// This recognizes only a price already identified by the model. It does not
// choose the last number in a message or infer intent from numeric tokens.
const pricePattern = `(?i)(?:за\s+)?(?:₽\s*)?([0-9]+(?:[.,][0-9]+)?)(?:\s*(?:рублях|рублей|рубля|рубль|рубли|руб\.?|р\.?|RUB|RUR|₽))?`

var exactPrice = regexp.MustCompile(`^` + pricePattern + `$`)
var commentPrices = regexp.MustCompile(pricePattern)
var alternativePrices = regexp.MustCompile(pricePattern + `\s+(?:или|либо|or)\s+` + pricePattern)

func creationComment(operation action, source string) (*string, error) {
	amount, err := expense.ParseAmount(operation.value("amount"))
	if err != nil {
		return nil, expense.ValidationError{"amount": "invalid"}
	}
	comment := operation.args["comment"]
	expression := operation.value("amount_text")
	if expression == "" {
		// Compatible endpoints may omit the optional source fragment. Accept a
		// clean comment, but never silently persist an apparent duplicated price.
		if !containsPrice(source, amount) || (comment != nil && containsPrice(*comment, amount)) {
			return nil, errDescription
		}
		return comment, nil
	}

	parts := exactPrice.FindStringSubmatch(expression)
	if len(parts) == 0 {
		return nil, errDescription
	}
	identified, err := expense.ParseAmount(strings.ReplaceAll(parts[1], ",", "."))
	if err != nil || identified != amount {
		return nil, errDescription
	}
	sourcePosition, count := expressionPosition(source, expression)
	if count != 1 || !completePriceExpression(source, expression) {
		return nil, errDescription
	}
	if comment == nil {
		return nil, nil
	}
	position, count := expressionPosition(*comment, expression)
	if count == 0 {
		// The model already separated the fields. Equal-valued model numbers
		// remain meaningful, e.g. "айфон 5" bought "за 5 рублей".
		remaining := source[:sourcePosition] + source[sourcePosition+len(expression):]
		if containsPrice(*comment, amount) && !strings.Contains(remaining, *comment) {
			return nil, errDescription
		}
		return comment, nil
	}
	if count != 1 {
		return nil, errDescription
	}
	before := strings.TrimRightFunc((*comment)[:position], unicode.IsSpace)
	after := strings.TrimLeftFunc((*comment)[position+len(expression):], unicode.IsSpace)
	cleaned := before + after
	if before != "" && after != "" {
		cleaned = before + " " + after
	}
	if cleaned == "" {
		return nil, nil
	}
	return &cleaned, nil
}

// Reject a proposed value that is one of explicit price alternatives. Other
// numbers remain descriptions/quantities; this does not pick a price for the model.
func ambiguousPrice(source string, amount expense.Amount) bool {
	for _, alternatives := range alternativePrices.FindAllStringSubmatch(source, -1) {
		for _, value := range alternatives[1:] {
			parsed, err := expense.ParseAmount(strings.ReplaceAll(value, ",", "."))
			if err == nil && parsed == amount {
				return true
			}
		}
	}
	return false
}

func matchesMarkedPrice(source string, amount expense.Amount) bool {
	for _, match := range priceMatches(source) {
		if !strings.HasPrefix(strings.ToLower(source[match[0]:match[1]]), "за") {
			continue
		}
		value := strings.ReplaceAll(source[match[2]:match[3]], ",", ".")
		marked, err := expense.ParseAmount(value)
		if err != nil || marked != amount {
			return false
		}
	}
	return true
}

func completePriceExpression(source, expression string) bool {
	for _, match := range priceMatches(source) {
		if source[match[0]:match[1]] == expression {
			return true
		}
	}
	return false
}

func containsPrice(comment string, amount expense.Amount) bool {
	for _, match := range priceMatches(comment) {
		value := strings.ReplaceAll(comment[match[2]:match[3]], ",", ".")
		parsed, err := expense.ParseAmount(value)
		if err == nil && parsed == amount {
			return true
		}
	}
	return false
}

// Retry after a rejected boundary: a word ending in "за" must not hide the
// following standalone price (for example, "глаза 500").
func priceMatches(text string) [][]int {
	var matches [][]int
	for offset := 0; offset < len(text); {
		match := commentPrices.FindStringSubmatchIndex(text[offset:])
		if match == nil {
			break
		}
		for index := range match {
			match[index] += offset
		}
		if expressionBoundary(text, match[0], match[1]) {
			matches = append(matches, match)
			offset = match[1]
		} else {
			_, width := utf8.DecodeRuneInString(text[match[0]:])
			offset = match[0] + width
		}
	}
	return matches
}

func expressionPosition(text, expression string) (int, int) {
	position := -1
	count := 0
	for offset := 0; offset < len(text); {
		index := strings.Index(text[offset:], expression)
		if index < 0 {
			break
		}
		start := offset + index
		end := start + len(expression)
		if expressionBoundary(text, start, end) {
			position = start
			count++
		}
		offset = end
	}
	return position, count
}

func expressionBoundary(text string, start, end int) bool {
	if start > 0 {
		previous, _ := utf8.DecodeLastRuneInString(text[:start])
		if unicode.IsLetter(previous) || unicode.IsNumber(previous) || strings.ContainsRune(".,+-/#", previous) {
			return false
		}
	}
	if end < len(text) {
		next, _ := utf8.DecodeRuneInString(text[end:])
		if unicode.IsLetter(next) || unicode.IsNumber(next) || strings.ContainsRune(".,/", next) {
			return false
		}
	}
	return true
}
