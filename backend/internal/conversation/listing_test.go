package conversation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/internal/llm"
)

func seedListingExpenses(t *testing.T, store *memoryStore, owner int64, dates ...string) {
	t.Helper()
	for _, date := range dates {
		_, err := store.repo.Create(context.Background(), expense.Expense{
			UserID: owner, Amount: 100, Category: expense.CategoryFood, SpentAt: expense.Date(date),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestListingPeriodsAndLatestFive(t *testing.T) {
	cases := []struct {
		text    string
		args    string
		heading string
		ids     []int64
		total   int
	}{
		{"мои траты", `{}`, "За всё время", []int64{1, 2, 3, 4, 5, 6, 7, 8}, 8},
		{"траты сегодня", `{"period":"today"}`, "За сегодня", []int64{1}, 1},
		{"траты за неделю", `{"period":"last_seven_days"}`, "За последние 7 дней", []int64{1, 2, 3}, 3},
		{"траты на этой неделе", `{"period":"this_week"}`, "За текущую неделю", []int64{1, 2}, 2},
		{"траты за месяц", `{"period":"this_month"}`, "За текущий месяц", []int64{1, 2, 3, 4, 5, 6}, 6},
		{"последние 5 трат", `{"limit":"5"}`, "За всё время", []int64{1, 2, 3, 4, 5}, 8},
	}
	for _, test := range cases {
		t.Run(test.text, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			// Insert oldest purchases last, so created_at cannot accidentally
			// replace the existing spent_at DESC, id DESC listing order.
			seedListingExpenses(t, store, 101, "2026-10-17", "2026-10-13", "2026-10-11", "2026-10-10", "2026-10-02", "2026-10-01", "2026-09-30", "2024-02-29")
			seedListingExpenses(t, store, 202, "2026-10-17")
			reply := handle(t, orchestrator, model, 1, 101, test.text, "list_expenses", test.args)
			header := strings.SplitN(plainReply(reply), "\n", 3)
			if len(header) < 2 || !strings.Contains(header[1], test.heading) || !strings.HasPrefix(header[1], fmt.Sprintf("%d ", test.total)) || len(listingIDs(t, reply)) != len(test.ids) {
				t.Fatal(reply)
			}
			if got := listingIDs(t, reply); !slices.Equal(got, test.ids) {
				t.Fatalf("want IDs %v, got %v", test.ids, got)
			}
		})
	}
}

func listingIDs(t *testing.T, reply string) []int64 {
	t.Helper()
	var ids []int64
	for _, line := range strings.Split(reply, "\n") {
		if !strings.Contains(line, " — <b>") || !strings.Contains(line, " · #") {
			continue
		}
		var id int64
		if _, err := fmt.Sscanf(line[strings.LastIndex(line, " · #")+len(" · #"):], "%d", &id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func TestListingEmptyWithAndWithoutDates(t *testing.T) {
	for _, args := range []string{`{}`, `{"period":"today"}`} {
		orchestrator, model, store := setup(t)
		seedListingExpenses(t, store, 202, "2026-10-17")
		reply := handle(t, orchestrator, model, 1, 101, "траты", "list_expenses", args)
		if !strings.Contains(plainReply(reply), "0 записей · ") || len(listingIDs(t, reply)) != 0 {
			t.Fatal(reply)
		}
		seedListingExpenses(t, store, 101, "2024-02-29")
		reply = handle(t, orchestrator, model, 2, 101, "траты сегодня", "list_expenses", `{"period":"today"}`)
		if !strings.Contains(reply, "За сегодня") || !strings.Contains(plainReply(reply), "0 записей · ") {
			t.Fatal(reply)
		}
	}
}

func TestListingPaginationAndIndependentRequests(t *testing.T) {
	orchestrator, model, store := setup(t)
	for range 25 {
		seedListingExpenses(t, store, 101, "2026-10-16")
	}
	seedListingExpenses(t, store, 101, "2024-02-29")
	seedListingExpenses(t, store, 202, "2026-10-17")
	first := handle(t, orchestrator, model, 1, 101, "траты за неделю", "list_expenses", `{"period":"last_seven_days"}`)
	if !strings.Contains(first, "Записи 1–20 из 25.") || len(listingIDs(t, first)) != 20 || !strings.Contains(first, "ещё") {
		t.Fatal(first)
	}
	second := handle(t, orchestrator, model, 2, 101, "ещё", "navigate_expenses", `{"direction":"next"}`)
	if !strings.Contains(second, "Записи 21–25 из 25.") || !slices.Equal(listingIDs(t, second), []int64{5, 4, 3, 2, 1}) || strings.Contains(second, "ещё") {
		t.Fatal(second)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimPrefix(model.request.Messages[0].Content, systemPrompt)), &data); err != nil {
		t.Fatal(err)
	}
	var previous map[string]any
	if err := json.Unmarshal(data["previous_listing"], &previous); err != nil {
		t.Fatal(err)
	}
	if previous["has_next"] != true || previous["has_previous"] != false || previous["limit"] != "20" || previous["date_from"] != "2026-10-11" || previous["query"] != nil {
		t.Fatalf("incorrect follow-up context: %v", previous)
	}
	for index, text := range []string{"мои траты", "мои расходы", "траты", "покажи расходы"} {
		reply := handle(t, orchestrator, model, int64(index+3), 101, text, "list_expenses", `{}`)
		if !strings.Contains(reply, "Записи 1–20 из 26.") || orchestrator.states[101].listing.DateFrom != "" || orchestrator.states[101].listing.NextOffset != 20 {
			t.Fatal("independent request inherited a previous date or offset:", reply)
		}
	}
	last := handle(t, orchestrator, model, 7, 101, "ещё", "navigate_expenses", `{"direction":"next"}`)
	if !strings.Contains(last, "Записи 21–26 из 26.") || !slices.Equal(listingIDs(t, last), []int64{5, 4, 3, 2, 1, 26}) {
		t.Fatal(last)
	}
	for _, id := range listingIDs(t, first) {
		if slices.Contains(listingIDs(t, second), id) {
			t.Fatal("pagination repeated a row")
		}
	}
	// B's context contains neither A's filters nor A's continuation offset.
	reply := handle(t, orchestrator, model, 8, 202, "мои расходы", "list_expenses", `{}`)
	if !slices.Equal(listingIDs(t, reply), []int64{27}) || strings.Contains(model.request.Messages[0].Content, `"previous_listing"`) {
		t.Fatal("listing context crossed owners:", reply)
	}
}

func TestIndependentListingUsesCurrentReferenceAfterClarification(t *testing.T) {
	orchestrator, model, store := setup(t)
	seedListingExpenses(t, store, 101, "2026-10-17", "2026-10-18")
	model.response = tool("clarify", `{"field":"amount"}`)
	original := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	if _, err := orchestrator.Handle(context.Background(), 1, 101, "кофе", original); err != nil {
		t.Fatal(err)
	}
	model.response = tool("list_expenses", `{"period":"today"}`)
	reply, err := orchestrator.Handle(context.Background(), 2, 101, "траты сегодня", original.AddDate(0, 0, 1))
	if err != nil || !slices.Equal(listingIDs(t, reply), []int64{2}) {
		t.Fatalf("listing reused an unrelated pending reference: %s, %v", reply, err)
	}
}

func TestListingOptionalDateAndFilters(t *testing.T) {
	cases := []struct {
		name string
		text string
		tool string
		args string
		want string
	}{
		{name: "my spending", text: "мои траты", tool: "list_expenses", args: `{}`, want: "🧾 Мои расходы\n3 записи · За всё время"},
		{name: "my expenses", text: "мои расходы", tool: "list_expenses", args: `{"period":null,"date_from":null,"date_to":null,"category":null,"query":null}`, want: "🧾 Мои расходы\n3 записи · За всё время"},
		{name: "all optional arguments null", text: "мои расходы", tool: "list_expenses", args: `{"period":null,"date_from":null,"date_to":null,"category":null,"query":null,"limit":null}`, want: "🧾 Мои расходы\n3 записи · За всё время"},
		{name: "spending", text: "траты", tool: "list_expenses", args: `{}`, want: "🧾 Мои расходы\n3 записи · За всё время"},
		{name: "show expenses", text: "покажи расходы", tool: "list_expenses", args: `{}`, want: "🧾 Мои расходы\n3 записи · За всё время"},
		{name: "all today", text: "покажи все расходы за сегодня", tool: "list_expenses", args: `{"period":"today","date_from":null,"date_to":null,"category":null,"query":null}`, want: "🧾 Мои расходы\n2 записи · За сегодня · 17 октября 2026"},
		{name: "explicit date", text: "покажи расходы за 2026-10-16", tool: "list_expenses", args: `{"period":null,"date_from":"2026-10-16","date_to":"2026-10-16","category":null,"query":null}`, want: "🧾 Мои расходы\n1 запись · 16 октября 2026"},
		{name: "relative yesterday", text: "покажи расходы за вчера", tool: "list_expenses", args: `{"period":"yesterday"}`, want: "🧾 Мои расходы\n1 запись · За вчера · 16 октября 2026"},
		{name: "category", text: "покажи расходы на транспорт", tool: "list_expenses", args: `{"category":"транспорт"}`, want: "🧾 Мои расходы\n1 запись · За всё время · 🚕 Транспорт"},
		{name: "literal comment", text: "покажи кофе", tool: "list_expenses", args: `{"query":"кофе"}`, want: "🧾 Мои расходы\n1 запись · За всё время · «кофе»"},
		{name: "empty result", text: "покажи расходы на жильё", tool: "list_expenses", args: `{"category":"жильё"}`, want: "🧾 Мои расходы\n0 записей · За всё время · 🏠 Жильё\nРасходы не найдены."},
		{name: "total today", text: "сколько потратил сегодня", tool: "total_expenses", args: `{}`, want: "📊 Всего: 550 ₽\n17 октября 2026"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			handle(t, orchestrator, model, 1, 101, "кофе 350", "create_expense", `{"amount":"350","category":"еда","spent_at":"today","comment":"кофе"}`)
			handle(t, orchestrator, model, 2, 101, "транспорт 200", "create_expense", `{"amount":"200","category":"транспорт","spent_at":"today"}`)
			handle(t, orchestrator, model, 3, 101, "еда 100", "create_expense", `{"amount":"100","category":"еда","spent_at":"yesterday"}`)
			handle(t, orchestrator, model, 4, 202, "покупки 900", "create_expense", `{"amount":"900","category":"покупки","spent_at":"today","comment":"private-owner-B"}`)
			reply := handle(t, orchestrator, model, 5, 101, test.text, test.tool, test.args)
			if !strings.HasPrefix(plainReply(reply), test.want) || strings.Contains(reply, "private-owner-B") || len(store.repo.items) != 4 {
				t.Fatalf("unexpected listing: %s", reply)
			}
			if string(model.request.ToolChoice) != `"required"` || model.request.ParallelToolCalls == nil || *model.request.ParallelToolCalls || !model.request.RequireExactlyOneToolCall {
				t.Fatal("conversation does not enforce single tool calling")
			}
		})
	}
}

func TestListingArgumentErrors(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{name: "forged owner", args: `{"user_id":"202"}`, want: invalidRequest},
		{name: "numeric category", args: `{"category":1}`, want: invalidRequest},
		{name: "empty period", args: `{"period":""}`, want: invalidRequest},
		{name: "duplicate null", args: `{"category":null,"category":"еда"}`, want: invalidRequest},
		{name: "invalid category", args: `{"category":"all"}`, want: invalidFilter},
		{name: "invalid period", args: `{"period":"all"}`, want: invalidFilter},
		{name: "invalid date", args: `{"date_from":"2026-02-30"}`, want: invalidFilter},
		{name: "reversed dates", args: `{"date_from":"2026-10-17","date_to":"2026-10-16"}`, want: invalidFilter},
		{name: "conflicting date modes", args: `{"period":"today","date_from":"2026-10-17","date_to":"2026-10-17"}`, want: invalidFilter},
		{name: "zero limit", args: `{"limit":"0"}`, want: invalidFilter},
		{name: "oversized page", args: `{"limit":"21"}`, want: invalidFilter},
		{name: "negative offset", args: `{"offset":"-1"}`, want: invalidRequest},
		{name: "huge offset", args: `{"offset":"2147483648"}`, want: invalidRequest},
		{name: "noncanonical limit", args: `{"limit":"05"}`, want: invalidFilter},
		{name: "numeric limit", args: `{"limit":5}`, want: invalidRequest},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			reply := handle(t, orchestrator, model, 1, 101, "покажи расходы", "list_expenses", test.args)
			if reply != test.want || len(store.repo.items) != 0 || storedReply(store.results[1]) != reply {
				t.Fatalf("reply=%s", reply)
			}
		})
	}
}

func TestListingThroughAdapterAndSafeFailures(t *testing.T) {
	const call = `{"id":"1","type":"function","function":{"name":"list_expenses","arguments":"{}"}}`
	cases := []struct {
		name   string
		text   string
		calls  string
		finish string
		want   string
	}{
		{name: "valid list", calls: call, finish: "tool_calls", want: "🧾 Мои расходы\n0 записей · За всё время\nРасходы не найдены. Попробуйте другой период или добавьте расход."},
		{name: "nonsense misclassified as list", text: "ыфваываы", calls: call, finish: "tool_calls", want: unknownIntentReply},
		{name: "explicit unknown intent", text: "asdfghjkl", calls: strings.Replace(call, "list_expenses", "unknown_intent", 1), finish: "tool_calls", want: unknownIntentReply},
		{name: "zero calls", finish: "stop", want: invalidModelResponse},
		{name: "multiple calls", calls: call + "," + strings.Replace(call, `"id":"1"`, `"id":"2"`, 1), finish: "tool_calls", want: invalidModelResponse},
		{name: "malformed arguments", calls: strings.Replace(call, `"arguments":"{}"`, `"arguments":"{private-argument"`, 1), finish: "tool_calls", want: invalidModelResponse},
		{name: "unknown tool", calls: strings.Replace(call, "list_expenses", "private-unknown-tool", 1), finish: "tool_calls", want: invalidRequest},
		{name: "unknown field", calls: strings.Replace(call, `"arguments":"{}"`, `"arguments":"{\"private-field\":\"private-argument\"}"`, 1), finish: "tool_calls", want: invalidRequest},
		{name: "invalid filter", calls: strings.Replace(call, `"arguments":"{}"`, `"arguments":"{\"period\":\"private-period\"}"`, 1), finish: "tool_calls", want: invalidFilter},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() {
				slog.SetDefault(previous)
			})
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var payload map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if string(payload["tool_choice"]) != `"required"` || string(payload["parallel_tool_calls"]) != "false" {
					t.Error("missing single tool controls on wire")
				}
				body := `{"choices":[{"message":{"role":"assistant","content":"private-response","tool_calls":[` + test.calls + `]},"finish_reason":"` + test.finish + `"}]}`
				if _, err := w.Write([]byte(body)); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			client, err := llm.New(llm.Config{BaseURL: server.URL, Model: "synthetic", Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			orchestrator, _, store := setup(t)
			orchestrator.model = client
			text := test.text
			if text == "" {
				text = "покажи расходы private-message"
			}
			for attempt := 0; attempt < 2; attempt++ {
				reply, err := orchestrator.Handle(context.Background(), 1, 101, text, time.Now())
				if err != nil || plainReply(reply) != test.want {
					t.Fatalf("unexpected safe reply: %s, error=%v", reply, err)
				}
			}
			if requests != 1 || len(store.results) != 1 || len(store.repo.items) != 0 {
				t.Fatal("failed or replayed request executed again")
			}
			if test.want == unknownIntentReply && store.repo.listCalls != 0 {
				t.Fatal("rejected intent still queried expenses")
			}
			if strings.Contains(logs.String(), "private-") {
				t.Fatal("diagnostics leaked model arguments, names, or message contents")
			}
			if test.want == invalidFilter && !strings.Contains(logs.String(), `"stage":"expense_filter"`) {
				t.Fatal("missing filter rejection diagnostic")
			}
			if test.want == invalidRequest && !strings.Contains(logs.String(), `"stage":"tool_arguments"`) {
				t.Fatal("missing argument rejection diagnostic")
			}
		})
	}
}

func TestReadToolSchemas(t *testing.T) {
	for _, tool := range toolsForModel() {
		if tool.Function.Name != "list_expenses" && tool.Function.Name != "total_expenses" {
			continue
		}
		var schema struct {
			Properties map[string]struct {
				Type []string `json:"type"`
				Enum []any    `json:"enum"`
			} `json:"properties"`
			Required             []string `json:"required"`
			AdditionalProperties bool     `json:"additionalProperties"`
		}
		if err := json.Unmarshal(tool.Function.Parameters, &schema); err != nil {
			t.Fatal(err)
		}
		fields := 5
		if tool.Function.Name == "list_expenses" {
			fields = 6
		}
		if !tool.Function.Strict || schema.AdditionalProperties || len(schema.Properties) != fields || len(schema.Required) != fields {
			t.Fatal("read schema must be strict with nullable optional arguments")
		}
		for field, property := range schema.Properties {
			if !contains(schema.Required, field) || !contains(property.Type, "string") || !contains(property.Type, "null") {
				t.Fatalf("filter %s cannot be omitted through null", field)
			}
			if field == "category" || field == "period" {
				nullAllowed := false
				for _, value := range property.Enum {
					if value == nil {
						nullAllowed = true
					}
				}
				if !nullAllowed {
					t.Fatalf("enum for %s excludes null", field)
				}
			}
		}
	}
}

func plainReply(reply string) string {
	return html.UnescapeString(strings.NewReplacer("<b>", "", "</b>", "", "<code>", "", "</code>", "").Replace(reply))
}
