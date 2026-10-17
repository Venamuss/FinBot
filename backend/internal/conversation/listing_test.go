package conversation

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/llm"
)

func TestListingDefaultDateAndFilters(t *testing.T) {
	cases := []struct {
		name string
		text string
		tool string
		args string
		want string
	}{
		{name: "my spending", text: "мои траты", tool: "list_expenses", args: `{}`, want: "Траты: показано 2 из 2"},
		{name: "my expenses", text: "мои расходы", tool: "list_expenses", args: `{"period":null,"date_from":null,"date_to":null,"category":null,"query":null}`, want: "Траты: показано 2 из 2"},
		{name: "all today", text: "покажи все расходы за сегодня", tool: "list_expenses", args: `{"period":"today","date_from":null,"date_to":null,"category":null,"query":null}`, want: "Траты: показано 2 из 2"},
		{name: "explicit date", tool: "list_expenses", args: `{"period":null,"date_from":"2026-10-16","date_to":"2026-10-16","category":null,"query":null}`, want: "Траты: показано 1 из 1"},
		{name: "relative yesterday", tool: "list_expenses", args: `{"period":"yesterday"}`, want: "Траты: показано 1 из 1"},
		{name: "category", tool: "list_expenses", args: `{"category":"транспорт"}`, want: "Траты: показано 1 из 1"},
		{name: "literal comment", tool: "list_expenses", args: `{"query":"кофе"}`, want: "Траты: показано 1 из 1"},
		{name: "empty result", tool: "list_expenses", args: `{"category":"жильё"}`, want: "Траты не найдены."},
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
		{name: "valid list", calls: call, finish: "tool_calls", want: "Траты не найдены."},
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
		if !tool.Function.Strict || schema.AdditionalProperties || len(schema.Properties) != 5 || len(schema.Required) != 5 {
			t.Fatal("read schema must be strict with five nullable filters")
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
