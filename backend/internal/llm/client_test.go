package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const textResponse = `{"choices":[{"message":{"role":"assistant","content":"ready"},"finish_reason":"stop"}]}`
const singleCall = `{"id":"call_1","type":"function","function":{"name":"record","arguments":"{\"amount\":\"350.00\",\"id\":9223372036854775807}"}}`

func toolResponse(calls string) string {
	return `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[` + calls + `]},"finish_reason":"tool_calls"}]}`
}

func testClient(t *testing.T, endpoint, key string, timeout time.Duration) *Client {
	t.Helper()
	client, err := New(Config{BaseURL: endpoint, Model: "test-model", APIKey: key, Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func textRequest() Request {
	return Request{Messages: []Message{{Role: "user", Content: "synthetic input"}}}
}

func writeResponse(t *testing.T, writer http.ResponseWriter, body string) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if _, err := io.WriteString(writer, body); err != nil {
		t.Error(err)
	}
}

func TestCompleteRequest(t *testing.T) {
	choices := []string{"", `"auto"`, `"none"`, `"required"`, `{"type":"function","function":{"name":"record"}}`}
	for _, key := range []string{"", "synthetic-key"} {
		for _, choice := range choices {
			name := fmt.Sprintf("key=%t/choice=%s", key != "", choice)
			t.Run(name, func(t *testing.T) {
				request := Request{
					Messages: []Message{
						{Role: "system", Content: "Use the provided schema."},
						{Role: "user", Content: "synthetic input"},
					},
					Tools: []Tool{{Type: "function", Function: Function{
						Name: "record", Description: "Record a synthetic item",
						Parameters: json.RawMessage(`{"type":"object","properties":{"amount":{"type":"string"}},"required":["amount"],"additionalProperties":false}`),
					}}},
					ToolChoice:                json.RawMessage(choice),
					RequireExactlyOneToolCall: true,
				}
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, incoming *http.Request) {
					requests.Add(1)
					if incoming.Method != http.MethodPost || incoming.URL.Path != "/api/v1/chat/completions" || incoming.URL.RawQuery != "" {
						t.Errorf("unexpected method or URL: %s %s", incoming.Method, incoming.URL)
					}
					if incoming.Header.Get("Content-Type") != "application/json" || incoming.Header.Get("Accept") != "application/json" {
						t.Error("JSON headers missing")
					}
					if key == "" {
						if _, present := incoming.Header["Authorization"]; present {
							t.Error("empty key sent an Authorization header")
						}
					} else if incoming.Header.Get("Authorization") != "Bearer "+key {
						t.Error("incorrect Authorization header")
					}
					var payload map[string]json.RawMessage
					if err := json.NewDecoder(incoming.Body).Decode(&payload); err != nil {
						t.Error(err)
						return
					}
					expectedFields := 3
					if choice != "" {
						expectedFields++
					}
					if len(payload) != expectedFields || string(payload["model"]) != `"test-model"` {
						t.Error("unexpected fields or configured model")
					}
					var messages []Message
					if err := json.Unmarshal(payload["messages"], &messages); err != nil || !reflect.DeepEqual(messages, request.Messages) {
						t.Error("messages changed")
					}
					var tools []Tool
					if err := json.Unmarshal(payload["tools"], &tools); err != nil || !reflect.DeepEqual(tools, request.Tools) {
						t.Error("tool schema changed")
					}
					if string(payload["tool_choice"]) != choice {
						t.Error("tool choice changed")
					}
					writeResponse(t, writer, toolResponse(singleCall))
				}))
				defer server.Close()
				client := testClient(t, server.URL+"/api/v1/", key, time.Second)
				response, err := client.Complete(context.Background(), request)
				if err != nil {
					t.Fatal(err)
				}
				if requests.Load() != 1 || response.Content != nil || response.FinishReason != "tool_calls" || len(response.ToolCalls) != 1 {
					t.Fatalf("unexpected response: %+v", response)
				}
				call := response.ToolCalls[0]
				if call.ID != "call_1" || call.Name != "record" || string(call.Arguments) != `{"amount":"350.00","id":9223372036854775807}` {
					t.Fatalf("tool call changed: %+v", call)
				}
			})
		}
	}
}

func TestCompleteResponses(t *testing.T) {
	secondCall := strings.Replace(singleCall, "call_1", "call_2", 1)
	type responseCase struct {
		name       string
		body       string
		exactlyOne bool
		want       error
	}
	tests := []responseCase{
		{name: "text", body: textResponse},
		{name: "unknown provider metadata", body: strings.Replace(textResponse, `"choices"`, `"usage":{"total_tokens":10},"choices"`, 1)},
		{name: "tool without content", body: strings.Replace(toolResponse(singleCall), `"content":null,`, "", 1)},
		{name: "text and tool", body: strings.Replace(toolResponse(singleCall), `"content":null`, `"content":"working"`, 1)},
		{name: "multiple allowed", body: toolResponse(singleCall + "," + secondCall)},
		{name: "unknown operation left to caller", body: strings.Replace(toolResponse(singleCall), "record", "unknown", 1)},
		{name: "length returned to caller", body: strings.Replace(textResponse, `"stop"`, `"length"`, 1)},
		{name: "filtered response", body: `{"choices":[{"message":{"role":"assistant","content":null},"finish_reason":"content_filter"}]}`},
		{name: "empty", body: "", want: ErrInvalidResponse},
		{name: "whitespace", body: " \n ", want: ErrInvalidResponse},
		{name: "invalid JSON", body: "{", want: ErrInvalidResponse},
		{name: "trailing JSON", body: textResponse + `{}`, want: ErrInvalidResponse},
		{name: "array", body: "[]", want: ErrInvalidResponse},
		{name: "null", body: "null", want: ErrInvalidResponse},
		{name: "scalar", body: "42", want: ErrInvalidResponse},
		{name: "missing choices", body: "{}", want: ErrInvalidResponse},
		{name: "empty choices", body: `{"choices":[]}`, want: ErrInvalidResponse},
		{name: "null choices", body: `{"choices":null}`, want: ErrInvalidResponse},
		{name: "choices object", body: `{"choices":{}}`, want: ErrInvalidResponse},
		{name: "multiple choices", body: `{"choices":[{},{}]}`, want: ErrInvalidResponse},
		{name: "missing message", body: `{"choices":[{"finish_reason":"stop"}]}`, want: ErrInvalidResponse},
		{name: "null choice", body: `{"choices":[null]}`, want: ErrInvalidResponse},
		{name: "message string", body: `{"choices":[{"message":"text"}]}`, want: ErrInvalidResponse},
		{name: "wrong role", body: strings.Replace(textResponse, "assistant", "user", 1), want: ErrInvalidResponse},
		{name: "missing role", body: strings.Replace(textResponse, `"role":"assistant",`, "", 1), want: ErrInvalidResponse},
		{name: "object content", body: strings.Replace(textResponse, `"ready"`, `{}`, 1), want: ErrInvalidResponse},
		{name: "array content", body: strings.Replace(textResponse, `"ready"`, `[]`, 1), want: ErrInvalidResponse},
		{name: "missing result", body: strings.Replace(textResponse, `"ready"`, `null`, 1), want: ErrInvalidResponse},
		{name: "missing finish reason", body: strings.Replace(textResponse, `,"finish_reason":"stop"`, "", 1), want: ErrInvalidResponse},
		{name: "unknown finish reason", body: strings.Replace(textResponse, `"stop"`, `"other"`, 1), want: ErrInvalidResponse},
		{name: "null finish reason", body: strings.Replace(textResponse, `"stop"`, `null`, 1), want: ErrInvalidResponse},
		{name: "error with success", body: strings.Replace(textResponse, `"choices"`, `"error":{"message":"private-provider-detail"},"choices"`, 1), want: ErrInvalidResponse},
		{name: "no tool calls required", body: textResponse, exactlyOne: true, want: ErrToolCallCount},
		{name: "multiple tools required one", body: toolResponse(singleCall + "," + secondCall), exactlyOne: true, want: ErrToolCallCount},
		{name: "empty tool calls", body: toolResponse(""), want: ErrInvalidResponse},
		{name: "missing tool calls", body: strings.Replace(textResponse, `"stop"`, `"tool_calls"`, 1), want: ErrInvalidResponse},
		{name: "tool calls object", body: strings.Replace(toolResponse(""), `"tool_calls":[]`, `"tool_calls":{}`, 1), want: ErrInvalidResponse},
		{name: "null tool calls", body: strings.Replace(toolResponse(""), `"tool_calls":[]`, `"tool_calls":null`, 1), want: ErrInvalidResponse},
		{name: "null tool call", body: toolResponse("null"), want: ErrInvalidResponse},
		{name: "empty tool call", body: toolResponse("{}"), want: ErrInvalidResponse},
		{name: "missing call ID", body: toolResponse(strings.Replace(singleCall, `"id":"call_1",`, "", 1)), want: ErrInvalidResponse},
		{name: "empty call ID", body: toolResponse(strings.Replace(singleCall, "call_1", " ", 1)), want: ErrInvalidResponse},
		{name: "duplicate call IDs", body: toolResponse(singleCall + "," + singleCall), want: ErrInvalidResponse},
		{name: "wrong call type", body: toolResponse(strings.Replace(singleCall, `"type":"function"`, `"type":"other"`, 1)), want: ErrInvalidResponse},
		{name: "missing function", body: toolResponse(`{"id":"1","type":"function"}`), want: ErrInvalidResponse},
		{name: "null function", body: toolResponse(`{"id":"1","type":"function","function":null}`), want: ErrInvalidResponse},
		{name: "empty function name", body: toolResponse(strings.Replace(singleCall, "record", " ", 1)), want: ErrInvalidResponse},
		{name: "truncated tools", body: strings.Replace(toolResponse(singleCall), `"finish_reason":"tool_calls"`, `"finish_reason":"length"`, 1), want: ErrInvalidResponse},
		{name: "inconsistent stop", body: strings.Replace(toolResponse(singleCall), `"finish_reason":"tool_calls"`, `"finish_reason":"stop"`, 1), want: ErrInvalidResponse},
	}
	for _, arguments := range []string{`null`, `{}`, `[]`, `42`, `""`, `"{"`, `"null"`, `"[]"`, `"1"`, `"{} {}"`} {
		body := toolResponse(`{"id":"1","type":"function","function":{"name":"record","arguments":` + arguments + `}}`)
		tests = append(tests, responseCase{name: "invalid arguments " + arguments, body: body, want: ErrInvalidResponse})
	}
	tests = append(tests, responseCase{name: "missing arguments", body: toolResponse(`{"id":"1","type":"function","function":{"name":"record"}}`), want: ErrInvalidResponse})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeResponse(t, writer, test.body)
			}))
			defer server.Close()
			client := testClient(t, server.URL, "", time.Second)
			request := textRequest()
			request.RequireExactlyOneToolCall = test.exactlyOne
			response, err := client.Complete(context.Background(), request)
			if !errors.Is(err, test.want) {
				t.Fatalf("want %v, got %v", test.want, err)
			}
			if err != nil && !reflect.DeepEqual(response, Response{}) {
				t.Fatal("invalid response exposed partial results")
			}
			if test.name == "text" && (response.Content == nil || *response.Content != "ready" || response.FinishReason != "stop") {
				t.Fatalf("incorrect text response: %+v", response)
			}
		})
	}
}

func TestParallelToolCallsWire(t *testing.T) {
	for _, mode := range []string{"default", "enabled", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			request := textRequest()
			if mode != "default" {
				enabled := mode == "enabled"
				request.ParallelToolCalls = &enabled
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				value, present := payload["parallel_tool_calls"]
				if mode == "default" {
					if present {
						t.Error("default parallel policy must be omitted")
					}
				} else if !present || string(value) != fmt.Sprint(mode == "enabled") {
					t.Error("explicit parallel policy lost on wire")
				}
				writeResponse(t, w, textResponse)
			}))
			defer server.Close()
			client := testClient(t, server.URL, "", time.Second)
			if _, err := client.Complete(context.Background(), request); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHTTPErrors(t *testing.T) {
	for _, status := range []int{200, 204, 301, 400, 401, 403, 404, 429, 500, 502, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				writer.WriteHeader(status)
				if status != http.StatusNoContent {
					writeResponse(t, writer, `{"error":{"message":"private-provider-detail synthetic-key"}}`)
				}
			}))
			defer server.Close()
			client := testClient(t, server.URL, "synthetic-key", time.Second)
			_, err := client.Complete(context.Background(), textRequest())
			want := ErrHTTP
			switch {
			case status == 200:
				want = ErrInvalidResponse
			case status == 401 || status == 403:
				want = ErrAuthentication
			case status == 429:
				want = ErrRateLimit
			case status >= 500:
				want = ErrProvider
			}
			if !errors.Is(err, want) || requests.Load() != 1 {
				t.Fatalf("want one request and %v, got %d / %v", want, requests.Load(), err)
			}
			if status != 200 {
				var httpError *HTTPError
				if !errors.As(err, &httpError) || httpError.StatusCode != status {
					t.Fatal("HTTP status lost")
				}
			}
			assertSafeError(t, err)
		})
	}
}

func assertSafeError(t *testing.T, err error) {
	t.Helper()
	for current := err; current != nil; current = errors.Unwrap(current) {
		for _, secret := range []string{"private-provider-detail", "synthetic-key", "synthetic input", "http://", "https://"} {
			if strings.Contains(fmt.Sprintf("%+v", current), secret) {
				t.Fatal("error exposed private request/provider data")
			}
		}
	}
}

func TestRedirectNotFollowed(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		redirected.Add(1)
		writeResponse(t, writer, textResponse)
	}))
	defer target.Close()
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				http.Redirect(writer, request, target.URL, status)
			}))
			defer server.Close()
			client := testClient(t, server.URL, "synthetic-key", time.Second)
			_, err := client.Complete(context.Background(), textRequest())
			if !errors.Is(err, ErrHTTP) || redirected.Load() != 0 {
				t.Fatalf("redirect followed or wrong error: %v", err)
			}
		})
	}
}

func TestResponseLimit(t *testing.T) {
	for _, streamed := range []bool{false, true} {
		for _, extra := range []int{0, 1} {
			t.Run(fmt.Sprintf("streamed=%t/extra=%d", streamed, extra), func(t *testing.T) {
				body := textResponse + strings.Repeat(" ", maxResponseBytes-len(textResponse)+extra)
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					if streamed {
						writer.(http.Flusher).Flush()
					} else {
						writer.Header().Set("Content-Length", fmt.Sprint(len(body)))
					}
					writeResponse(t, writer, body)
				}))
				defer server.Close()
				client := testClient(t, server.URL, "", time.Second)
				_, err := client.Complete(context.Background(), textRequest())
				if extra == 0 && err != nil {
					t.Fatal(err)
				}
				if extra > 0 && !errors.Is(err, ErrResponseTooLarge) {
					t.Fatalf("want size limit error, got %v", err)
				}
			})
		}
	}
}

func TestCancellationAndTimeout(t *testing.T) {
	for _, duringBody := range []bool{false, true} {
		for _, mode := range []string{"cancel", "deadline", "client timeout"} {
			t.Run(fmt.Sprintf("body=%t/%s", duringBody, mode), func(t *testing.T) {
				started := make(chan struct{})
				release := make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					if duringBody {
						writer.(http.Flusher).Flush()
					}
					close(started)
					select {
					case <-request.Context().Done():
					case <-release:
					}
				}))
				defer server.Close()
				defer close(release)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				timeout := 2 * time.Second
				want := context.DeadlineExceeded
				switch mode {
				case "cancel":
					want = context.Canceled
				case "deadline":
					var deadlineCancel context.CancelFunc
					ctx, deadlineCancel = context.WithTimeout(ctx, 100*time.Millisecond)
					defer deadlineCancel()
				case "client timeout":
					timeout = 100 * time.Millisecond
				}
				client := testClient(t, server.URL, "synthetic-key", timeout)
				result := make(chan error, 1)
				go func() {
					_, err := client.Complete(ctx, textRequest())
					result <- err
				}()
				if mode == "cancel" {
					select {
					case <-started:
						cancel()
					case <-time.After(3 * time.Second):
						t.Fatal("request did not arrive")
					}
				}
				select {
				case err := <-result:
					if !errors.Is(err, want) {
						t.Fatalf("want %v, got %v", want, err)
					}
					assertSafeError(t, err)
				case <-time.After(3 * time.Second):
					t.Fatal("request did not terminate")
				}
			})
		}
	}
}

func TestNetworkFailures(t *testing.T) {
	t.Run("connection refused", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		server.Close()
		client := testClient(t, server.URL+"/private-provider-detail", "synthetic-key", time.Second)
		_, err := client.Complete(context.Background(), textRequest())
		if !errors.Is(err, ErrNetwork) {
			t.Fatalf("want network error, got %v", err)
		}
		assertSafeError(t, err)
	})
	t.Run("incomplete body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Length", "10000")
			writeResponse(t, writer, textResponse)
		}))
		defer server.Close()
		client := testClient(t, server.URL, "synthetic-key", time.Second)
		_, err := client.Complete(context.Background(), textRequest())
		if !errors.Is(err, ErrNetwork) {
			t.Fatalf("want network error, got %v", err)
		}
		assertSafeError(t, err)
	})
}
