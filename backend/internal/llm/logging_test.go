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
		name    string
		status  int
		body    string
		err     error
		invalid bool
		wantErr error
		oneTool bool
		count   int
		finish  string
	}{
		{name: "text", status: 200, body: strings.ReplaceAll(textResponse, "ready", "private-response")},
		{name: "tool", status: 200, body: toolResponse(privateTool)},
		{name: "zero tools", status: 200, body: textResponse, oneTool: true, wantErr: ErrToolCallCount, count: 0, finish: "stop"},
		{name: "multiple tools", status: 200, body: toolResponse(privateTool + "," + strings.Replace(privateTool, "call_1", "call_2", 1)), oneTool: true, wantErr: ErrToolCallCount, count: 2, finish: "tool_calls"},
		{name: "malformed tool arguments", status: 200, body: toolResponse(strings.Replace(privateTool, `"arguments":"`, `"arguments":"private-invalid-json`, 1)), wantErr: ErrInvalidResponse, count: 1, finish: "tool_calls"},
		{name: "authentication", status: 401, body: "private-provider-body", wantErr: ErrAuthentication},
		{name: "rate limit", status: 429, body: "private-provider-body", wantErr: ErrRateLimit},
		{name: "provider failure", status: 503, body: "private-provider-body", wantErr: ErrProvider},
		{name: "redirect", status: 302, body: "private-provider-body", wantErr: ErrHTTP},
		{name: "malformed response", status: 200, body: "private-malformed-body", wantErr: ErrInvalidResponse},
		{name: "untrusted finish reason", status: 200, body: strings.ReplaceAll(textResponse, "stop", "private-finish-reason"), wantErr: ErrInvalidResponse},
		{name: "size limit", status: 200, body: strings.Repeat("x", maxResponseBytes+1), wantErr: ErrResponseTooLarge},
		{name: "network", err: errors.New("private-network-error https://private-proxy:private-password@proxy.invalid"), wantErr: ErrNetwork},
		{name: "timeout", err: os.ErrDeadlineExceeded, wantErr: context.DeadlineExceeded},
		{name: "invalid request", invalid: true, wantErr: ErrInvalidRequest},
		{name: "cancellation", wantErr: context.Canceled},
		{name: "deadline", wantErr: context.DeadlineExceeded},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			t.Cleanup(func() {
				slog.SetDefault(previous)
			})
			client := testClient(t, "http://provider.invalid/private-endpoint", "private-api-key", time.Second)
			client.model = "private-model"
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
				if records[index]["component"] != "llm" {
					t.Fatal("missing LLM component")
				}
			}
			if records[0]["msg"] != "LLM request started" || records[0]["level"] != "INFO" || records[0]["message_count"] != float64(len(request.Messages)) || records[0]["tool_count"] != float64(len(request.Tools)) {
				t.Fatalf("unexpected start event: %v", records[0])
			}
			terminal := records[1]
			if test.finish != "" && (terminal["tool_call_count"] != float64(test.count) || terminal["finish_reason"] != test.finish) {
				t.Fatalf("missing rejected response metadata: %v", terminal)
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
				if terminal["status_code"] != float64(httpError.StatusCode) {
					t.Fatal("missing HTTP status")
				}
			} else if terminal["status_code"] != nil {
				t.Fatal("unexpected HTTP status")
			}
		})
	}
}
