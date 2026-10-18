package botresponse

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/Venamuss/FinBot/internal/expense"
)

func sample() expense.Expense {
	text := "Кафе"
	return expense.Expense{ID: 15, UserID: 101, Amount: 50000, Category: expense.CategoryFood, SpentAt: "2026-10-18", Comment: &text}
}

// Decode the emitted subset as XML to reject broken tags/entities and assert
// that untrusted text cannot introduce tags or attributes.
func assertHTML(t *testing.T, message string) string {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader("<root>" + message + "</root>"))
	var plain strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid HTML: %v\n%s", err, message)
		}
		switch value := token.(type) {
		case xml.StartElement:
			if (value.Name.Local != "root" && value.Name.Local != "b" && value.Name.Local != "code") || len(value.Attr) != 0 {
				t.Fatalf("unexpected tag: %v", value)
			}
		case xml.CharData:
			plain.Write(value)
		}
	}
	units := len(utf16.Encode([]rune(plain.String())))
	if units != Length(message) || units > MessageLimit {
		t.Fatalf("incorrect or excessive visible length: parsed=%d formatter=%d", units, Length(message))
	}
	if strings.Contains(message, "\n\n\n") || strings.HasPrefix(message, "\n") || strings.HasSuffix(message, "\n") {
		t.Fatal("excess vertical spacing:", message)
	}
	return plain.String()
}

func TestCompactConfirmations(t *testing.T) {
	before := sample()
	item := before
	item.Category = expense.CategoryEntertainment
	item.Amount = 12020
	text := "Пиво"
	item.Comment = &text
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"create", Created(item), "✅ <b>Расход добавлен · #15</b>\n🎮 Пиво — <b>120,20 ₽</b>\nРазвлечения · 18 окт 2026"},
		{"update", Updated(before, item), "✏️ <b>Расход обновлён · #15</b>\n🎮 Пиво — <b>120,20 ₽</b>\nРазвлечения · 18 окт 2026 · Изменено: сумма, категория, комментарий"},
		{"delete", Deleted(item), "🗑 <b>Расход удалён · #15</b>\n🎮 Пиво — <b>120,20 ₽</b>\nРазвлечения · 18 окт 2026"},
		{"unchanged", Updated(item, item), "✏️ <b>Расход обновлён · #15</b>\n🎮 Пиво — <b>120,20 ₽</b>\nРазвлечения · 18 окт 2026 · Без изменений"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			assertHTML(t, test.got)
			if test.got != test.want || strings.Count(test.got, "\n") != 2 || strings.Contains(test.got, "\n\n") {
				t.Fatal(test.got)
			}
		})
	}
	item.SpentAt = "2026-10-17"
	item.Comment = nil
	updated := Updated(before, item)
	if !strings.Contains(updated, "Изменено: сумма, категория, дата, комментарий") || !strings.Contains(updated, "17 окт 2026") {
		t.Fatal(updated)
	}
}

func TestEmptyCommentsAndCategoryFallback(t *testing.T) {
	for _, text := range []*string{nil, new(string), new(" \n\t "), new("еда")} {
		item := sample()
		item.Comment = text
		for _, message := range []string{Created(item), Updated(item, item), Deleted(item)} {
			assertHTML(t, message)
			if strings.Count(message, "\n") != 2 || strings.Contains(message, "Еда ·") || strings.Contains(message, "📝") {
				t.Fatal("duplicated category or empty comment line:", message)
			}
		}
	}
	item := sample()
	item.Comment = nil
	want := "✅ <b>Расход добавлен · #15</b>\n🍔 Еда — <b>500 ₽</b>\n18 окт 2026"
	if Created(item) != want {
		t.Fatal(Created(item))
	}
	page, _ := Page(expense.Page{Items: []expense.Expense{item}, Total: 1}, expense.OwnerFilter{}, "", 0)
	if !strings.Contains(page, "🍔 Еда — <b>500 ₽</b> · #15") {
		t.Fatal(page)
	}
}

func TestCompactListExact(t *testing.T) {
	first := sample()
	second := first
	second.ID = 14
	second.Comment = nil
	third := first
	third.ID = 13
	third.SpentAt = "2026-10-17"
	page := expense.Page{Items: []expense.Expense{first, second, third}, Total: 3}
	got, shown := Page(page, expense.OwnerFilter{}, "", 0)
	want := "🧾 <b>Мои расходы</b>\n3 записи · За всё время\n\n<b>18 октября 2026</b>\n🍔 Кафе — <b>500 ₽</b> · #15\n🍔 Еда — <b>500 ₽</b> · #14\n\n<b>17 октября 2026</b>\n🍔 Кафе — <b>500 ₽</b> · #13"
	assertHTML(t, got)
	if shown != 3 || got != want || strings.Contains(got, "Записи") || strings.Contains(got, "Показано") || strings.Contains(got, "Найдено") {
		t.Fatal(got)
	}
	if strings.Count(got, "\n\n") != 2 || strings.Count(got, "\n") != 8 {
		t.Fatal("unexpected list height:", got)
	}
	page.Total = 11
	got, _ = Page(page, expense.OwnerFilter{}, "", 5)
	if !strings.HasSuffix(got, "Записи 6–8 из 11. Далее: «ещё».") {
		t.Fatal(got)
	}
	last, _ := Page(expense.Page{Items: []expense.Expense{third}, Total: 11}, expense.OwnerFilter{}, "", 10)
	if !strings.HasSuffix(last, "Записи 11–11 из 11.") || strings.Contains(last, "ещё") {
		t.Fatal(last)
	}
}

func TestSummariesEmptyPagesAndFilters(t *testing.T) {
	filter := expense.OwnerFilter{DateFrom: "2026-10-01", DateTo: "2026-10-18", Category: expense.CategoryFood}
	summary := Total("6000.10", filter, "this_month")
	want := "📊 <b>Всего: 6 000,10 ₽</b>\nЗа текущий месяц · С 1 октября 2026 по 18 октября 2026 · 🍔 Еда"
	assertHTML(t, summary)
	if summary != want {
		t.Fatal(summary)
	}
	for _, total := range []int64{0, 20} {
		reply, shown := Page(expense.Page{Total: total}, filter, "this_month", 20)
		assertHTML(t, reply)
		if shown != 0 || strings.Contains(reply, "ещё") || strings.Contains(reply, "\n\n") || strings.Count(reply, "\n") != 2 {
			t.Fatal(reply)
		}
	}
	text := `чай < кофе & "сок"`
	filter.Query = text
	reply, _ := Page(expense.Page{Items: []expense.Expense{sample()}, Total: 1}, filter, "", 0)
	if plain := assertHTML(t, reply); !strings.Contains(plain, "«"+text+"»") {
		t.Fatal(reply)
	}
	for count, want := range map[int64]string{0: "0 записей", 1: "1 запись", 2: "2 записи", 5: "5 записей", 11: "11 записей", 14: "14 записей", 21: "21 запись", 22: "22 записи", 111: "111 записей"} {
		if recordCount(count) != want {
			t.Fatal(recordCount(count))
		}
	}
}

func TestSelectionExact(t *testing.T) {
	item := sample()
	got := Selection([]expense.Expense{item}, false)
	want := "<b>Какую трату выбрать? Ответьте номером.</b>\n1. 🍔 Кафе — <b>500 ₽</b> · 18 окт 2026 · #15"
	assertHTML(t, got)
	if got != want || strings.Contains(got, "\n\n") {
		t.Fatal(got)
	}
	if got := Selection([]expense.Expense{item}, true); !strings.HasPrefix(got, "<b>Предыдущая сумма не совпадает.") {
		t.Fatal(got)
	}
}

func TestMoneyPrecision(t *testing.T) {
	for input, want := range map[string]string{
		"0.00": "0 ₽", "0.01": "0,01 ₽", "0.10": "0,10 ₽", "500.00": "500 ₽", "6000.50": "6 000,50 ₽",
		"999999999999.99": "999 999 999 999,99 ₽", "922337203685477580800.01": "922 337 203 685 477 580 800,01 ₽",
	} {
		if got := Money(input); got != want {
			t.Fatalf("%s: %s != %s", input, got, want)
		}
	}
}

func TestDatesAndCategories(t *testing.T) {
	for input, want := range map[expense.Date]string{
		"2026-10-18": "18 октября 2026", "2024-02-29": "29 февраля 2024", "2025-12-31": "31 декабря 2025", "2026-01-01": "1 января 2026",
	} {
		if got := dateLabel(input, false); got != want {
			t.Fatalf("%s != %s", got, want)
		}
	}
	if dateLabel("2024-02-29", true) != "29 фев 2024" {
		t.Fatal("incorrect abbreviated month")
	}
	for _, category := range []expense.Category{
		expense.CategoryFood, expense.CategoryTransport, expense.CategoryHousing,
		expense.CategoryEntertainment, expense.CategoryHealth, expense.CategoryShopping, expense.CategoryOther,
	} {
		item := sample()
		item.Category = category
		icon, name := categoryLabel(category)
		if icon == "" || !strings.Contains(Created(item), icon+" Кафе") || !strings.Contains(Created(item), name+" · ") {
			t.Fatalf("missing category indicator: %s", category)
		}
	}
}

func TestUntrustedHTMLAndCyrillic(t *testing.T) {
	text := `Кофе </b><a href="tg://user?id=202">я</a> & <code>#99</code> 😀 ' " &lt;b&gt;`
	item := sample()
	item.Comment = &text
	filter := expense.OwnerFilter{Query: text}
	page, _ := Page(expense.Page{Items: []expense.Expense{item}, Total: 1}, filter, "", 0)
	for _, message := range []string{Created(item), Updated(sample(), item), Deleted(item), page, Total("0.00", filter, ""), Selection([]expense.Expense{item}, true)} {
		plain := assertHTML(t, message)
		if !strings.Contains(plain, text) || strings.Contains(message, `<a href=`) {
			t.Fatal(message)
		}
	}
}

func TestLongCommentsRemainWholeWhenTheyFit(t *testing.T) {
	for _, length := range []int{250, 700, 3500} {
		text := strings.Repeat("я", length) + "конец"
		item := sample()
		item.Comment = &text
		page, shown := Page(expense.Page{Items: []expense.Expense{item}, Total: 1}, expense.OwnerFilter{}, "", 0)
		if shown != 1 {
			t.Fatal("fitting record not displayed")
		}
		for _, message := range []string{Created(item), Updated(sample(), item), Deleted(item), page, Selection([]expense.Expense{item}, false)} {
			plain := assertHTML(t, message)
			if !strings.Contains(plain, text) || strings.Contains(message, "сокращён") {
				t.Fatal("fitting comment shortened:", message)
			}
		}
	}
	text := strings.Repeat("я", 3000)
	item := sample()
	item.Comment = &text
	second := item
	second.ID = 14
	page, shown := Page(expense.Page{Items: []expense.Expense{item, second}, Total: 2}, expense.OwnerFilter{}, "", 0)
	if shown != 1 || !strings.Contains(page, text) || strings.Contains(page, "сокращён") || !strings.Contains(page, "ещё") {
		t.Fatal("page should defer a whole record before shortening a comment")
	}
}

func TestLongPagesAndMessageLimits(t *testing.T) {
	for _, text := range []string{strings.Repeat("😀", 5000), strings.Repeat("<&>\"'", 5000)} {
		items := make([]expense.Expense, 20)
		for index := range items {
			items[index] = sample()
			items[index].ID = int64(20 - index)
			items[index].Comment = &text
			items[index].SpentAt = expense.Date(fmt.Sprintf("2026-10-%02d", 20-index))
		}
		filter := expense.OwnerFilter{Query: text}
		offset := 0
		for offset < len(items) {
			reply, shown := Page(expense.Page{Items: items[offset:], Total: 20}, filter, "", offset)
			assertHTML(t, reply)
			if shown != 1 || !strings.Contains(reply, expenseID(items[offset])) || !strings.HasSuffix(reply, shortenedNotice) {
				t.Fatal("oversized record was lost or shortening was silent:", reply)
			}
			offset += shown
			if strings.Contains(reply, "ещё") != (offset < len(items)) {
				t.Fatal("incorrect continuation")
			}
		}
		selection := Selection(items[:10], false)
		if strings.Count(selection, "\n") != 11 || strings.Contains(selection, "\n\n") {
			t.Fatal("candidate rows lost or separated by blank lines")
		}
		for _, message := range []string{Created(items[0]), Updated(sample(), items[0]), Deleted(items[0]), selection, Total("0.00", filter, "")} {
			assertHTML(t, message)
			if !strings.Contains(message, "сокращён") {
				t.Fatal("missing shortening disclosure")
			}
		}
	}
	for _, units := range []int{4095, 4096, 4097} {
		message := "<b>" + strings.Repeat("&amp;", units-2) + "😀</b>"
		if Length(message) != units {
			t.Fatal("incorrect entity or astral length")
		}
	}
}

func TestEscapedTextBoundaryAndMultilineComments(t *testing.T) {
	for _, value := range []string{strings.Repeat("я", 240), strings.Repeat("😀", 120)} {
		if got, shortened := escapedText(value, 240); got != value || shortened {
			t.Fatal("text fitting the budget was shortened")
		}
	}
	if got, shortened := escapedText(strings.Repeat("я", 241), 240); got != strings.Repeat("я", 239)+"…" || !shortened {
		t.Fatal("incorrect ellipsis budget")
	}
	item := sample()
	item.Comment = new("Кафе\n\nи\tчай")
	got := Created(item)
	if !strings.Contains(got, "Кафе и чай") || strings.Count(got, "\n") != 2 {
		t.Fatal("comment introduced vertical spacing:", got)
	}
}

func TestRenderedMessageBoundaries(t *testing.T) {
	for _, kind := range []string{"creation", "list", "selection"} {
		for _, target := range []int{4095, 4096, 4097} {
			item := sample()
			item.Comment = new("я")
			var initial string
			switch kind {
			case "creation":
				initial = Created(item)
			case "list":
				initial, _ = Page(expense.Page{Items: []expense.Expense{item}, Total: 1}, expense.OwnerFilter{}, "", 0)
			case "selection":
				initial = Selection([]expense.Expense{item}, false)
			}
			item.Comment = new(strings.Repeat("я", target-Length(initial)+1))
			var message string
			switch kind {
			case "creation":
				message = Created(item)
			case "list":
				message, _ = Page(expense.Page{Items: []expense.Expense{item}, Total: 1}, expense.OwnerFilter{}, "", 0)
			case "selection":
				message = Selection([]expense.Expense{item}, false)
			}
			assertHTML(t, message)
			if target <= MessageLimit {
				if Length(message) != target || strings.Contains(message, shortenedNotice) {
					t.Fatalf("%s: fitting %d-unit message shortened", kind, target)
				}
			} else if !strings.HasSuffix(message, shortenedNotice) {
				t.Fatalf("%s: oversized message shortened silently", kind)
			}
		}
	}
}

func TestEmptyAndErrorResponsesCompact(t *testing.T) {
	reply, shown := Page(expense.Page{}, expense.OwnerFilter{}, "", 0)
	want := "🧾 <b>Мои расходы</b>\n0 записей · За всё время\nРасходы не найдены. Попробуйте другой период или добавьте расход."
	if reply != want || shown != 0 {
		t.Fatal(reply)
	}
	for _, message := range []string{InvalidRequest, InvalidModelResponse, InvalidFilter, StaleSelection, Unavailable, ProcessingFailure, InvalidAmount} {
		assertHTML(t, message)
		if strings.Contains(message, "\n") {
			t.Fatal("unexpected line break in error:", message)
		}
	}
}
