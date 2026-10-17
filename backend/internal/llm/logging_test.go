package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/observability"
)

type loggingTransport struct {
	status int
	body   string
	err    error
	calls  int
}

func (transport *loggingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.calls++
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	if transport.err != nil {
		return nil, transport.err
	}
	return &http.Response{
		StatusCode: transport.status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(transport.body)),
	}, nil
}

func TestCompleteLogging(t *testing.T) {
	privateTool := strings.ReplaceAll(singleCall, "record", "private-result-tool")
	privateTool = strings.ReplaceAll(privateTool, "350.00", "private-argument")
	cases := []struct {
		name     string
		status   int
		body     string
		err      error
		invalid  bool
		wantErr  error
		oneTool  bool
		count    int
		finish   string
		category string
	}{
		{name: "text", status: 200, body: strings.ReplaceAll(textResponse, "ready", "private-response")},
		{name: "tool", status: 200, body: toolResponse(privateTool)},
		{name: "zero tools", category: "invalid_tool_call", status: 200, body: textResponse, oneTool: true, wantErr: ErrToolCallCount, count: 0, finish: "stop"},
		{name: "multiple tools", category: "invalid_tool_call", status: 200, body: toolResponse(privateTool + "," + strings.Replace(privateTool, "call_1", "call_2", 1)), oneTool: true, wantErr: ErrToolCallCount, count: 2, finish: "tool_calls"},
		{name: "malformed tool arguments", category: "protocol", status: 200, body: toolResponse(strings.Replace(privateTool, `"arguments":"`, `"arguments":"private-invalid-json`, 1)), wantErr: ErrInvalidResponse, count: 1, finish: "tool_calls"},
		{name: "authentication", category: "authentication", status: 401, body: "private-provider-body", wantErr: ErrAuthentication},
		{name: "rate limit", category: "rate_limit", status: 429, body: "private-provider-body", wantErr: ErrRateLimit},
		{name: "provider failure", category: "api", status: 503, body: "private-provider-body", wantErr: ErrProvider},
		{name: "redirect", category: "api", status: 302, body: "private-provider-body", wantErr: ErrHTTP},
		{name: "malformed response", category: "protocol", status: 200, body: "private-malformed-body", wantErr: ErrInvalidResponse},
		{name: "untrusted finish reason", category: "protocol", status: 200, body: strings.ReplaceAll(textResponse, "stop", "private-finish-reason"), wantErr: ErrInvalidResponse},
		{name: "size limit", category: "protocol", status: 200, body: strings.Repeat("x", maxResponseBytes+1), wantErr: ErrResponseTooLarge},
		{name: "network", category: "network", err: errors.New("private-network-error https://private-proxy:private-password@proxy.invalid"), wantErr: ErrNetwork},
		{name: "timeout", category: "timeout", err: os.ErrDeadlineExceeded, wantErr: context.DeadlineExceeded},
		{name: "invalid request", category: "validation", invalid: true, wantErr: ErrInvalidRequest},
		{name: "cancellation", wantErr: context.Canceled},
		{name: "deadline", category: "timeout", wantErr: context.DeadlineExceeded},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
			t.Cleanup(func() {
				slog.SetDefault(previous)
			})
			client := testClient(t, "http://provider.invalid/private-endpoint", "private-api-key", time.Second)
			client.model = "synthetic-model"
			transport := &loggingTransport{status: test.status, body: test.body, err: test.err}
			client.httpClient.Transport = transport
			request := Request{
				Messages: []Message{{Role: "system", Content: "private-system-prompt"}, {Role: "user", Content: "private-user-prompt"}},
				Tools: []Tool{{Type: "function", Function: Function{
					Name: "private-request-tool", Description: "private-description", Parameters: json.RawMessage(`{"private-schema":true}`),
				}}},
			}
			if test.invalid {
				request = Request{}
			}
			request.RequireExactlyOneToolCall = test.oneTool
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch test.name {
			case "cancellation":
				cancel()
			case "deadline":
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer deadlineCancel()
			}
			response, err := client.Complete(ctx, request)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("unexpected error: %v", err)
			}
			if test.invalid && transport.calls != 0 || !test.invalid && transport.calls != 1 {
				t.Fatalf("unexpected request count: %d", transport.calls)
			}
			if strings.Contains(output.String(), "private-") || strings.Contains(output.String(), "provider.invalid") || strings.Contains(output.String(), "proxy.invalid") {
				t.Fatal("logs exposed private request/provider/transport data")
			}
			lines := strings.Split(strings.TrimSpace(output.String()), "\n")
			if len(lines) != 2 {
				t.Fatalf("expected start and terminal events: %s", &output)
			}
			records := make([]map[string]any, len(lines))
			for index, line := range lines {
				if err := json.Unmarshal([]byte(line), &records[index]); err != nil {
					t.Fatal(err)
				}
				if records[index]["model"] != "synthetic-model" || records[index]["operation"] != "chat_completions" {
					t.Fatal("missing model/operation")
				}
				if records[index]["component"] != "llm" {
					t.Fatal("missing LLM component")
				}
			}
			if records[0]["msg"] != "LLM request started" || records[0]["level"] != "DEBUG" || records[0]["message_count"] != float64(len(request.Messages)) || records[0]["tool_count"] != float64(len(request.Tools)) {
				t.Fatalf("unexpected start event: %v", records[0])
			}
			terminal := records[1]
			if test.finish != "" && (terminal["tool_call_count"] != float64(test.count) || terminal["finish_reason"] != test.finish) {
				t.Fatalf("missing rejected response metadata: %v", terminal)
			}
			if test.wantErr != nil && terminal["error_category"] != test.category && test.wantErr != context.Canceled {
				t.Fatal("missing error classification")
			}
			if terminal["duration_ms"] == nil {
				t.Fatal("missing duration")
			}
			switch {
			case test.wantErr == nil:
				if terminal["msg"] != "LLM request completed" || terminal["level"] != "INFO" || terminal["finish_reason"] != response.FinishReason || terminal["tool_call_count"] != float64(len(response.ToolCalls)) {
					t.Fatalf("unexpected completion: %v", terminal)
				}
			case errors.Is(test.wantErr, context.Canceled):
				if terminal["msg"] != "LLM request canceled" || terminal["level"] != "INFO" || terminal["error"] != nil {
					t.Fatalf("cancellation logged as failure: %v", terminal)
				}
			default:
				if terminal["msg"] != "LLM request failed" || terminal["level"] != "ERROR" || terminal["error"] == nil {
					t.Fatalf("unexpected failure: %v", terminal)
				}
			}
			var httpError *HTTPError
			if errors.As(err, &httpError) {
				if terminal["http_status"] != float64(httpError.StatusCode) {
					t.Fatal("missing HTTP status")
				}
			} else if terminal["http_status"] != nil {
				t.Fatal("unexpected HTTP status")
			}
		})
	}
}

func TestUsageLoggingAndCorrelation(t *testing.T) {
	cases := []struct {
		name, usage string
		present     bool
	}{
		{"counts", `{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"private-detail":"private-value"}`, true},
		{"zero counts", `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`, true},
		{"missing", `null`, false},
		{"empty", `{}`, false},
		{"partial", `{"total_tokens":15}`, false},
		{"negative", `{"prompt_tokens":-1,"completion_tokens":3,"total_tokens":2}`, false},
		{"invalid", `{"prompt_tokens":"private-value"}`, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			t.Cleanup(func() {
				slog.SetDefault(previous)
			})
			client := testClient(t, "http://provider.invalid", "private-key", time.Second)
			body := strings.TrimSuffix(textResponse, "}") + `,"usage":` + test.usage + `}`
			client.httpClient.Transport = &loggingTransport{status: 200, body: body}
			ctx := observability.WithUpdateID(context.Background(), 77)
			response, err := client.Complete(ctx, Request{Messages: []Message{{Role: "user", Content: "private-message"}}})
			if err != nil {
				t.Fatal(err)
			}
			if (response.Usage != nil) != test.present || strings.Contains(output.String(), "private-") || strings.Count(output.String(), "\n") != 1 {
				t.Fatal("usage validation/privacy or default log volume incorrect")
			}
			var record map[string]any
			if err := json.Unmarshal(output.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if record["update_id"] != float64(77) || record["model"] != "test-model" || record["duration_ms"] == nil {
				t.Fatal("missing LLM correlation/cost metadata")
			}
			if test.present {
				if record["prompt_tokens"] != float64(response.Usage.PromptTokens) || record["completion_tokens"] != float64(response.Usage.CompletionTokens) || record["total_tokens"] != float64(response.Usage.TotalTokens) {
					t.Fatal("missing usage counters")
				}
			} else if record["total_tokens"] != nil {
				t.Fatal("invalid/missing usage logged as real counts")
			}
		})
	}
}

func TestRejectedToolResponseRetainsUsage(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
	})
	client := testClient(t, "http://provider.invalid", "private-key", time.Second)
	body := strings.TrimSuffix(textResponse, "}") + `,"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}}`
	client.httpClient.Transport = &loggingTransport{status: 200, body: body}
	_, err := client.Complete(context.Background(), Request{
		Messages:                  []Message{{Role: "user", Content: "private-message"}},
		RequireExactlyOneToolCall: true,
	})
	if !errors.Is(err, ErrToolCallCount) {
		t.Fatal("tool count validation changed")
	}
	var failure *ResponseError
	if !errors.As(err, &failure) || failure.Usage == nil || failure.Usage.TotalTokens != 15 {
		t.Fatal("validated usage lost on rejected tool response")
	}
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["total_tokens"] != float64(15) || record["error_category"] != "invalid_tool_call" || strings.Contains(output.String(), "private-") {
		t.Fatal("usage/error logging incorrect or unsafe")
	}
}
