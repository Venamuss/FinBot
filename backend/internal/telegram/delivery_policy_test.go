package telegram

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTerminalDeliveryClassification(t *testing.T) {
	for _, test := range []struct {
		name     string
		failure  requestError
		terminal bool
	}{
		{name: "bad message", failure: requestError{reason: "http_status", httpStatus: 400}, terminal: true},
		{name: "blocked recipient", failure: requestError{reason: "http_status", httpStatus: 403}, terminal: true},
		{name: "API chat unavailable", failure: requestError{reason: "api_error", httpStatus: 200, apiCode: 400}, terminal: true},
		{name: "API forbidden", failure: requestError{reason: "api_error", httpStatus: 200, apiCode: 403}, terminal: true},
		{name: "HTTP rate limit", failure: requestError{reason: "http_status", httpStatus: 429}},
		{name: "API rate limit", failure: requestError{reason: "api_error", httpStatus: 200, apiCode: 429}},
		{name: "conflicting rate limit", failure: requestError{reason: "http_status", httpStatus: 403, apiCode: 429}},
		{name: "HTTP server error", failure: requestError{reason: "http_status", httpStatus: 503}},
		{name: "API server error", failure: requestError{reason: "api_error", httpStatus: 200, apiCode: 500}},
		{name: "authentication", failure: requestError{reason: "http_status", httpStatus: 401}},
		{name: "route unavailable", failure: requestError{reason: "http_status", httpStatus: 404}},
		{name: "network", failure: requestError{reason: "network"}},
		{name: "timeout", failure: requestError{reason: "timeout"}},
		{name: "malformed", failure: requestError{reason: "invalid_response", httpStatus: 200}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.failure.terminalDelivery() != test.terminal {
				t.Fatal("incorrect delivery classification")
			}
		})
	}
}

func TestResponseRetryAfter(t *testing.T) {
	for _, test := range []struct {
		name    string
		header  string
		seconds int64
		want    time.Duration
	}{
		{name: "absent"},
		{name: "Telegram parameters", seconds: 3, want: 3 * time.Second},
		{name: "HTTP header", header: "4", want: 4 * time.Second},
		{name: "larger parameter", header: "2", seconds: 3, want: 3 * time.Second},
		{name: "larger header", header: "4", seconds: 3, want: 4 * time.Second},
		{name: "negative", header: "-1", seconds: -2},
		{name: "overflow", header: "9223372036854775807", seconds: 9223372036854775807},
		{name: "malformed header", header: "private-header", seconds: 2, want: 2 * time.Second},
		{name: "expired date", header: time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := responseRetryAfter(test.header, test.seconds); got != test.want {
				t.Fatalf("delay=%s want=%s", got, test.want)
			}
		})
	}

	header := time.Now().Add(time.Minute).UTC().Format(http.TimeFormat)
	if delay := responseRetryAfter(header, 0); delay < 58*time.Second || delay > time.Minute {
		t.Fatal("future HTTP date did not delay retries")
	}
	if delay := retryDelayFor(&requestError{retryAfter: time.Millisecond}, time.Second); delay != time.Second {
		t.Fatal("server hint shortened the normal backoff")
	}
}

func TestRateLimitMetadataAndCancellation(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"private-response","parameters":{"retry_after":2}}`))
			}))
			defer server.Close()
			client := testClient(server)
			retry, err := client.request(context.Background(), "sendMessage", []byte(`{}`), nil)
			var failure *requestError
			if !retry || !errors.As(err, &failure) || failure.retryAfter != 2*time.Second || failure.terminalDelivery() {
				t.Fatal("rate-limit guidance or retryability was lost")
			}

			// A long server hint must remain interruptible rather than hold shutdown.
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			started := time.Now()
			err = client.Send(ctx, 101, "synthetic response")
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) >= time.Second {
				t.Fatal("rate-limit wait ignored cancellation")
			}
		})
	}
}
