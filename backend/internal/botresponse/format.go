// Package botresponse renders deterministic, escaped Telegram HTML from expense
// results. It performs no queries and has no dependency on transport or the LLM.
package botresponse

import (
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/Venamuss/FinBot/internal/expense"
)

const MessageLimit = 4096

const (
	InvalidRequest       = "Не удалось понять запрос. Уточните сумму, дату или нужную трату."
	InvalidModelResponse = "Не удалось понять запрос. Напишите одним сообщением, например «мои расходы за сегодня»."
	InvalidFilter        = "Не удалось определить период или фильтры. Уточните запрос, например «мои расходы за сегодня»."
	StaleSelection       = "Трата не найдена или была изменена. Укажите нужную трату заново."
	Unavailable          = "Сейчас не удаётся обработать запрос. Попробуйте позже новым сообщением."
	ProcessingFailure    = "Не удалось получить ответ. Пожалуйста, подождите."
	InvalidAmount        = "Укажите положительную сумму с точностью до копеек, например 350,50."
)

// Money formats decimal strings, including aggregate values larger than int64.
// Fractional hundredths are retained whenever nonzero; integers omit ,00.
func Money(value string) string {
	whole, fraction, _ := strings.Cut(value, ".")
	var grouped strings.Builder
	for index, digit := range whole {
		if index > 0 && (len(whole)-index)%3 == 0 {
			grouped.WriteByte(' ')
		}
		grouped.WriteRune(digit)
	}
	if fraction != "" && fraction != "00" {
		grouped.WriteByte(',')
		grouped.WriteString(fraction)
	}
	return html.EscapeString(grouped.String()) + " ₽"
}

// Shared separators keep cards and choices single-spaced, with one blank line
// reserved for list date groups.
const (
	lineBreak       = "\n"
	groupBreak      = "\n\n"
	separator       = " · "
	shortenedNotice = "Текст сокращён; полностью — в панели."
)

func dateLabel(value expense.Date, short bool) string {
	date, err := time.Parse(time.DateOnly, string(value))
	if err != nil {
		return html.EscapeString(string(value))
	}
	months := [...]string{"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"}
	if short {
		months = [...]string{"янв", "фев", "мар", "апр", "мая", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}
	}
	return fmt.Sprintf("%d %s %d", date.Day(), months[date.Month()-1], date.Year())
}

func categoryLabel(category expense.Category) (string, string) {
	switch category {
	case expense.CategoryFood:
		return "🍔", "Еда"
	case expense.CategoryTransport:
		return "🚕", "Транспорт"
	case expense.CategoryHousing:
		return "🏠", "Жильё"
	case expense.CategoryEntertainment:
		return "🎮", "Развлечения"
	case expense.CategoryHealth:
		return "💊", "Здоровье"
	case expense.CategoryShopping:
		return "🛍", "Покупки"
	case expense.CategoryOther:
		return "📦", "Другое"
	default:
		return "", string(category)
	}
}

// escapedText keeps a label on one logical line and shortens only to the supplied
// message budget. Callers must disclose shortening. Escape after cutting so HTML
// entities and UTF-16 surrogate pairs are never cut midway.
func escapedText(value string, limit int) (string, bool) {
	value = strings.Join(strings.Fields(value), " ")
	if limit <= 0 {
		return "", value != ""
	}
	units := 0
	previewEnd := len(value)
	for index, char := range value {
		units += utf16.RuneLen(char)
		if units > limit-1 && previewEnd == len(value) {
			previewEnd = index
		}
		if units > limit {
			return html.EscapeString(value[:previewEnd]) + "…", true
		}
	}
	return html.EscapeString(value), false
}

func expenseLabel(item expense.Expense) string {
	if item.Comment != nil && strings.TrimSpace(*item.Comment) != "" {
		return *item.Comment
	}
	_, name := categoryLabel(item.Category)
	return name
}

func expenseID(item expense.Expense) string {
	return "#" + strconv.FormatInt(item.ID, 10)
}

func expenseLine(item expense.Expense, limit int) (string, bool) {
	icon, _ := categoryLabel(item.Category)
	label, shortened := escapedText(expenseLabel(item), limit)
	if icon != "" {
		label = icon + " " + label
	}
	return label + " — <b>" + Money(item.Amount.String()) + "</b>", shortened
}

func confirmation(symbol, title string, item expense.Expense, changes string) string {
	_, category := categoryLabel(item.Category)
	footer := dateLabel(item.SpentAt, true)
	if !strings.EqualFold(strings.TrimSpace(expenseLabel(item)), category) {
		footer = html.EscapeString(category) + separator + footer
	}
	if changes != "" {
		footer += separator + changes
	}
	heading := symbol + " <b>" + html.EscapeString(title) + separator + expenseID(item) + "</b>"
	// Reserve fixed fields first, adding a disclosure only if shortening is
	// necessary. Typical confirmations are exactly 3 lines.
	emptyLine, _ := expenseLine(item, 0)
	budget := MessageLimit - Length(heading+lineBreak+emptyLine+lineBreak+footer)
	line, shortened := expenseLine(item, budget)
	if shortened {
		budget -= Length(lineBreak + shortenedNotice)
		line, shortened = expenseLine(item, budget)
	}
	reply := strings.Join([]string{heading, line, footer}, lineBreak)
	if shortened {
		reply += lineBreak + shortenedNotice
	}
	return reply
}

func Created(item expense.Expense) string {
	return confirmation("✅", "Расход добавлен", item, "")
}

func Updated(before, after expense.Expense) string {
	var changes []string
	if before.Amount != after.Amount {
		changes = append(changes, "сумма")
	}
	if before.Category != after.Category {
		changes = append(changes, "категория")
	}
	if before.SpentAt != after.SpentAt {
		changes = append(changes, "дата")
	}
	if commentValue(before.Comment) != commentValue(after.Comment) {
		changes = append(changes, "комментарий")
	}
	changed := "Без изменений"
	if len(changes) > 0 {
		changed = "Изменено: " + strings.Join(changes, ", ")
	}
	return confirmation("✏️", "Расход обновлён", after, changed)
}

func commentValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func Deleted(item expense.Expense) string {
	return confirmation("🗑", "Расход удалён", item, "")
}

func filterLabel(filter expense.OwnerFilter, period string) string {
	label := "За всё время"
	periods := map[string]string{
		"today": "За сегодня", "yesterday": "За вчера",
		"last_seven_days": "За последние 7 дней", "this_week": "За текущую неделю", "this_month": "За текущий месяц",
	}
	if value, ok := periods[period]; ok {
		label = value
	}
	if filter.DateFrom != "" && filter.DateFrom == filter.DateTo {
		label = dateLabel(filter.DateFrom, false)
	} else if filter.DateFrom != "" || filter.DateTo != "" {
		label = ""
		if filter.DateFrom != "" {
			label = "С " + dateLabel(filter.DateFrom, false)
		}
		if filter.DateTo != "" {
			label += " по " + dateLabel(filter.DateTo, false)
		}
		label = strings.TrimSpace(label)
	}
	if value, ok := periods[period]; ok && filter.DateFrom != "" {
		label = value + separator + label
	}
	if filter.Category != "" {
		icon, name := categoryLabel(filter.Category)
		label += separator + strings.TrimSpace(icon+" "+html.EscapeString(name))
	}
	if filter.Query != "" {
		query, shortened := escapedText(filter.Query, 512)
		label += separator + "«" + query + "»"
		if shortened {
			label += " (фильтр сокращён)"
		}
	}
	return label
}

func Total(total string, filter expense.OwnerFilter, period string) string {
	return "📊 <b>Всего: " + Money(total) + "</b>" + lineBreak + filterLabel(filter, period)
}

func recordCount(total int64) string {
	word := "записей"
	if total%100 < 11 || total%100 > 14 {
		switch total % 10 {
		case 1:
			word = "запись"
		case 2, 3, 4:
			word = "записи"
		}
	}
	return fmt.Sprintf("%d %s", total, word)
}

// Page preserves query order and advances only by complete displayed records.
// First reduce the page, keeping comments intact. Only a record too large to fit
// on its own receives an explicitly disclosed preview.
func Page(page expense.Page, filter expense.OwnerFilter, period string, offset int) (string, int) {
	header := "🧾 <b>Мои расходы</b>" + lineBreak + recordCount(page.Total) + separator + filterLabel(filter, period)
	for shown := len(page.Items); shown >= 0; shown-- {
		reply := renderPage(page, header, shown, offset, MessageLimit)
		if Length(reply) <= MessageLimit {
			return reply, shown
		}
		if shown == 1 {
			// A zero-label skeleton includes the shortening notice and all other
			// fields, giving an exact remaining budget independent of escaping.
			base := renderPage(page, header, shown, offset, 0)
			budget := MessageLimit - Length(base)
			return renderPage(page, header, shown, offset, budget), shown
		}
	}
	return InvalidFilter, 0
}

func renderPage(page expense.Page, header string, shown, offset, labelLimit int) string {
	if shown == 0 {
		message := "На этой странице расходов нет."
		if page.Total == 0 {
			message = "Расходы не найдены. Попробуйте другой период или добавьте расход."
		}
		return header + lineBreak + message
	}
	sections := []string{header}
	var group []string
	var previous expense.Date
	shortened := false
	for _, item := range page.Items[:shown] {
		if item.SpentAt != previous {
			if len(group) > 0 {
				sections = append(sections, strings.Join(group, lineBreak))
			}
			group = []string{"<b>" + dateLabel(item.SpentAt, false) + "</b>"}
			previous = item.SpentAt
		}
		line, cut := expenseLine(item, labelLimit)
		shortened = shortened || cut
		group = append(group, line+separator+expenseID(item))
	}
	sections = append(sections, strings.Join(group, lineBreak))
	reply := strings.Join(sections, groupBreak)
	if offset > 0 || int64(offset+shown) < page.Total {
		reply += lineBreak + fmt.Sprintf("Записи %d–%d из %d.", offset+1, offset+shown, page.Total)
		if int64(offset+shown) < page.Total {
			reply += " Далее: «ещё»."
		}
	}
	if shortened {
		reply += lineBreak + shortenedNotice
	}
	return reply
}

func Selection(candidates []expense.Expense, confirmAmount bool) string {
	heading := "Какую трату выбрать? Ответьте номером."
	if confirmAmount {
		heading = "Предыдущая сумма не совпадает. Подтвердите выбор номером 1 или отмените действие."
	}
	heading = "<b>" + heading + "</b>"
	reply := renderSelection(heading, candidates, MessageLimit)
	if Length(reply) <= MessageLimit || len(candidates) == 0 {
		return reply
	}
	base := renderSelection(heading, candidates, 0)
	budget := (MessageLimit - Length(base)) / len(candidates)
	return renderSelection(heading, candidates, budget)
}

func renderSelection(heading string, candidates []expense.Expense, labelLimit int) string {
	lines := []string{heading}
	shortened := false
	for index, item := range candidates {
		line, cut := expenseLine(item, labelLimit)
		shortened = shortened || cut
		lines = append(lines, fmt.Sprintf("%d. %s · %s · %s", index+1, line, dateLabel(item.SpentAt, true), expenseID(item)))
	}
	if shortened {
		lines = append(lines, shortenedNotice)
	}
	return strings.Join(lines, lineBreak)
}

// Length counts visible UTF-16 units after parsing our fixed HTML vocabulary.
// Escaped user tags are decoded only after removing formatter-owned tags.
func Length(message string) int {
	plain := strings.NewReplacer("<b>", "", "</b>", "", "<code>", "", "</code>", "").Replace(message)
	units := 0
	for _, char := range html.UnescapeString(plain) {
		units += utf16.RuneLen(char)
	}
	return units
}
