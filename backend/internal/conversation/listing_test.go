package conversation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
		{"мои траты", `{}`, "Все ваши расходы", []int64{1, 2, 3, 4, 5, 6, 7, 8}, 8},
		{"траты сегодня", `{"period":"today"}`, "Расходы за сегодня", []int64{1}, 1},
		{"траты за неделю", `{"period":"last_seven_days"}`, "Расходы за последние 7 дней", []int64{1, 2, 3}, 3},
		{"траты на этой неделе", `{"period":"this_week"}`, "Расходы за текущую неделю", []int64{1, 2}, 2},
		{"траты за месяц", `{"period":"this_month"}`, "Расходы за текущий месяц", []int64{1, 2, 3, 4, 5, 6}, 6},
		{"последние 5 трат", `{"limit":"5"}`, "Все ваши расходы", []int64{1, 2, 3, 4, 5}, 8},
	}
	for _, test := range cases {
		t.Run(test.text, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			// Insert oldest purchases last, so created_at cannot accidentally
			// replace the existing spent_at DESC, id DESC listing order.
			seedListingExpenses(t, store, 101, "2026-10-17", "2026-10-13", "2026-10-11", "2026-10-10", "2026-10-02", "2026-10-01", "2026-09-30", "2024-02-29")
			seedListingExpenses(t, store, 202, "2026-10-17")
			reply := handle(t, orchestrator, model, 1, 101, test.text, "list_expenses", test.args)
			if !strings.HasPrefix(reply, test.heading) || !strings.Contains(reply, fmt.Sprintf("показано %d из %d", len(test.ids), test.total)) {
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
		if !strings.HasPrefix(line, "#") {
			continue
		}
		var id int64
		if _, err := fmt.Sscanf(line, "#%d", &id); err != nil {
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
		if !strings.Contains(reply, "показано 0 из 0\nТраты не найдены.") || len(listingIDs(t, reply)) != 0 {
			t.Fatal(reply)
		}
		seedListingExpenses(t, store, 101, "2024-02-29")
		reply = handle(t, orchestrator, model, 2, 101, "траты сегодня", "list_expenses", `{"period":"today"}`)
		if !strings.HasPrefix(reply, "Расходы за сегодня") || !strings.Contains(reply, "показано 0 из 0\nТраты не найдены.") {
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
	if !strings.Contains(first, "показано 20 из 25") || len(listingIDs(t, first)) != 20 || !strings.Contains(first, "ещё") {
		t.Fatal(first)
	}
	second := handle(t, orchestrator, model, 2, 101, "ещё", "list_expenses", `{"date_from":"2026-10-11","date_to":"2026-10-17","offset":"20"}`)
	if !strings.Contains(second, "показано 5 из 25") || !slices.Equal(listingIDs(t, second), []int64{5, 4, 3, 2, 1}) || strings.Contains(second, "ещё") {
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
	if previous["next_offset"] != "20" || previous["limit"] != "20" || previous["date_from"] != "2026-10-11" || previous["query"] != nil {
		t.Fatalf("incorrect follow-up context: %v", previous)
	}
	for index, text := range []string{"мои траты", "мои расходы", "траты", "покажи расходы"} {
		reply := handle(t, orchestrator, model, int64(index+3), 101, text, "list_expenses", `{}`)
		if !strings.HasPrefix(reply, "Все ваши расходы: показано 20 из 26") || orchestrator.states[101].listing.DateFrom != "" || orchestrator.states[101].listing.NextOffset != 20 {
			t.Fatal("independent request inherited a previous date or offset:", reply)
		}
	}
	last := handle(t, orchestrator, model, 7, 101, "ещё", "list_expenses", `{"offset":"20"}`)
	if !strings.Contains(last, "показано 6 из 26") || !slices.Equal(listingIDs(t, last), []int64{5, 4, 3, 2, 1, 26}) {
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
		{name: "my spending", text: "мои траты", tool: "list_expenses", args: `{}`, want: "Все ваши расходы: показано 3 из 3"},
		{name: "my expenses", text: "мои расходы", tool: "list_expenses", args: `{"period":null,"date_from":null,"date_to":null,"category":null,"query":null}`, want: "Все ваши расходы: показано 3 из 3"},
		{name: "all optional arguments null", text: "мои расходы", tool: "list_expenses", args: `{"period":null,"date_from":null,"date_to":null,"category":null,"query":null,"limit":null,"offset":null}`, want: "Все ваши расходы: показано 3 из 3"},
		{name: "spending", text: "траты", tool: "list_expenses", args: `{}`, want: "Все ваши расходы: показано 3 из 3"},
		{name: "show expenses", text: "покажи расходы", tool: "list_expenses", args: `{}`, want: "Все ваши расходы: показано 3 из 3"},
		{name: "all today", text: "покажи все расходы за сегодня", tool: "list_expenses", args: `{"period":"today","date_from":null,"date_to":null,"category":null,"query":null}`, want: "Расходы за сегодня с 2026-10-17 по 2026-10-17: показано 2 из 2"},
		{name: "explicit date", tool: "list_expenses", args: `{"period":null,"date_from":"2026-10-16","date_to":"2026-10-16","category":null,"query":null}`, want: "Ваши расходы с 2026-10-16 по 2026-10-16: показано 1 из 1"},
		{name: "relative yesterday", tool: "list_expenses", args: `{"period":"yesterday"}`, want: "Расходы за вчера с 2026-10-16 по 2026-10-16: показано 1 из 1"},
		{name: "category", tool: "list_expenses", args: `{"category":"транспорт"}`, want: "Все ваши расходы; категория: транспорт: показано 1 из 1"},
		{name: "literal comment", tool: "list_expenses", args: `{"query":"кофе"}`, want: "Все ваши расходы; описание содержит: «кофе»: показано 1 из 1"},
		{name: "empty result", tool: "list_expenses", args: `{"category":"жильё"}`, want: "Все ваши расходы; категория: жильё: показано 0 из 0\nТраты не найдены."},
		{name: "total today", tool: "total_expenses", args: `{}`, want: "Всего: 550.00"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			handle(t, orchestrator, model, 1, 101, "synthetic", "create_expense", `{"amount":"350","category":"еда","spent_at":"today","comment":"кофе"}`)
			handle(t, orchestrator, model, 2, 101, "synthetic", "create_expense", `{"amount":"200","category":"транспорт","spent_at":"today"}`)
			handle(t, orchestrator, model, 3, 101, "synthetic", "create_expense", `{"amount":"100","category":"еда","spent_at":"yesterday"}`)
			handle(t, orchestrator, model, 4, 202, "synthetic", "create_expense", `{"amount":"900","category":"покупки","spent_at":"today","comment":"private-owner-B"}`)
			reply := handle(t, orchestrator, model, 5, 101, test.text, test.tool, test.args)
			if !strings.HasPrefix(reply, test.want) || strings.Contains(reply, "private-owner-B") || len(store.repo.items) != 4 {
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
		{name: "negative offset", args: `{"offset":"-1"}`, want: invalidFilter},
		{name: "huge offset", args: `{"offset":"2147483648"}`, want: invalidFilter},
		{name: "noncanonical limit", args: `{"limit":"05"}`, want: invalidFilter},
		{name: "numeric limit", args: `{"limit":5}`, want: invalidRequest},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			reply := handle(t, orchestrator, model, 1, 101, "synthetic", "list_expenses", test.args)
			if reply != test.want || len(store.repo.items) != 0 || store.results[1] != reply {
				t.Fatalf("reply=%s", reply)
			}
		})
	}
}

func TestListingThroughAdapterAndSafeFailures(t *testing.T) {
	const call = `{"id":"1","type":"function","function":{"name":"list_expenses","arguments":"{}"}}`
	cases := []struct {
		name   string
		calls  string
		finish string
		want   string
	}{
		{name: "valid list", calls: call, finish: "tool_calls", want: "Все ваши расходы: показано 0 из 0\nТраты не найдены."},
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
			for attempt := 0; attempt < 2; attempt++ {
				reply, err := orchestrator.Handle(context.Background(), 1, 101, "private-message", time.Now())
				if err != nil || reply != test.want {
					t.Fatalf("unexpected safe reply: %s, error=%v", reply, err)
				}
			}
			if requests != 1 || len(store.results) != 1 || len(store.repo.items) != 0 {
				t.Fatal("failed or replayed request executed again")
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
			fields = 7
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
