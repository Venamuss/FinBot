package telegram

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAPIRequestDiagnostics(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		reason   string
		apiCode  int
		attempts int
		send     bool
		timeout  bool
		network  bool
	}{
		{name: "invalid token", status: 401, body: `{"ok":false,"description":"private-body"}`, reason: "http_status", attempts: 1},
		{name: "polling conflict", status: 409, body: `{"ok":false,"description":"private-body"}`, reason: "http_status", attempts: 1},
		{name: "blocked bot", status: 403, body: `{"ok":false,"description":"private-body"}`, reason: "http_status", attempts: 1, send: true},
		{name: "rate limit", status: 429, body: `{"ok":false,"description":"private-body"}`, reason: "http_status", attempts: 3},
		{name: "server unavailable", status: 503, body: "private-body", reason: "http_status", attempts: 3},
		{name: "API envelope error", status: 200, body: `{"ok":false,"error_code":429,"description":"private-body"}`, reason: "api_error", apiCode: 429, attempts: 3},
		{name: "malformed response", status: 200, body: "private-body", reason: "invalid_response", attempts: 1},
		{name: "timeout", reason: "timeout", attempts: 3, timeout: true},
		{name: "network", reason: "network", attempts: 3, network: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			output := captureBotLogs(t)
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if test.timeout {
					select {
					case <-r.Context().Done():
					case <-release:
					}
					return
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			// An unread POST body can delay server-side cancellation detection.
			// Release stalled handlers before Close waits for them.
			defer close(release)
			if test.network {
				server.Close()
			}
			client := testClient(server)
			client.httpClient.Timeout = time.Second
			if test.timeout {
				client.httpClient.Timeout = 20 * time.Millisecond
			}
			var err error
			method := "getUpdates"
			if test.send {
				method = "sendMessage"
				err = client.Send(context.Background(), 101, "private-response")
			} else {
				_, err = client.Updates(context.Background(), 0)
			}
			if !errors.Is(err, ErrAPI) || err.Error() != ErrAPI.Error() {
				t.Fatal("API error identity or redaction changed")
			}
			attempts := 0
			for _, record := range decodeBotLogs(t, output) {
				if record["msg"] != "Telegram API request failed" {
					continue
				}
				attempts++
				if record["level"] != "WARN" || record["method"] != method || record["reason"] != test.reason || record["http_status"] != float64(test.status) || record["api_error_code"] != float64(test.apiCode) || record["attempt"] != float64(attempts) {
					t.Fatalf("incorrect diagnostic metadata: %v", record)
				}
				if record["retryable"] != (test.attempts > 1) {
					t.Fatal("incorrect retry classification")
				}
			}
			if attempts != test.attempts {
				t.Fatalf("logged attempts=%d want=%d", attempts, test.attempts)
			}
			for _, private := range []string{"private-body", "private-response", "bot-token", server.URL} {
				if strings.Contains(output.String(), private) {
					t.Fatal("sensitive data leaked into API diagnostics")
				}
			}
		})
	}
}

func TestCanceledRequestHasNoFailureDiagnostic(t *testing.T) {
	output := captureBotLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New("private-token").Updates(ctx, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost")
	}
	for _, record := range decodeBotLogs(t, output) {
		if record["level"] == "WARN" || record["level"] == "ERROR" {
			t.Fatal("normal cancellation logged as a failure")
		}
	}
}
