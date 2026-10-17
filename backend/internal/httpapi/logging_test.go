package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRequestLogging(t *testing.T) {
	cases := []struct {
		name, method, path, body, category, level string
		status                                    int
		err                                       error
	}{
		{name: "success", method: "GET", path: "/api/expenses", status: 200, category: "none", level: "INFO"},
		{name: "validation", method: "GET", path: "/api/expenses?private-field=private-value", status: 400, category: "validation", level: "WARN"},
		{name: "invalid body", method: "POST", path: "/api/expenses", body: "private-body", status: 400, category: "invalid_json", level: "WARN"},
		{name: "not found", method: "GET", path: "/private-path", status: 404, category: "not_found", level: "WARN"},
		{name: "internal", method: "GET", path: "/api/expenses", err: errors.New("private-password private-expense"), status: 500, category: "application", level: "ERROR"},
		{name: "database", method: "GET", path: "/api/expenses", err: &pgconn.PgError{Code: "23514", Detail: "private-row", Message: "private-sql"}, status: 500, category: "database", level: "ERROR"},
		{name: "timeout", method: "GET", path: "/api/expenses", err: context.DeadlineExceeded, status: 500, category: "timeout", level: "ERROR"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
			t.Cleanup(func() {
				slog.SetDefault(previous)
			})
			handler := New(expense.NewService(&fakeRepo{err: test.err}), time.Second)
			req := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer private-key")
			req.Header.Set("X-Request-ID", "private-request-id")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != test.status {
				t.Fatalf("status %d, want %d", response.Code, test.status)
			}
			if strings.Count(output.String(), "\n") != 1 || strings.Contains(output.String(), "private-") {
				t.Fatal("request log duplicated or exposed private input")
			}
			var record map[string]any
			if err := json.Unmarshal(output.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if record["http_status"] != float64(test.status) || record["level"] != test.level || record["error_category"] != test.category || record["request_id"] == nil || record["request_id"] == "" || record["duration_ms"] == nil || record["component"] != "http" || record["operation"] != "request" {
				t.Fatalf("incorrect request metadata: %v", record)
			}
			if test.name == "success" && record["route"] != "GET /api/expenses" {
				t.Fatalf("missing route template: %v", record)
			}
		})
	}
}

type deadlineRepo struct{ *fakeRepo }

func (repo deadlineRepo) List(ctx context.Context, _ expense.Filter) (expense.Page, error) {
	<-ctx.Done()
	return expense.Page{}, ctx.Err()
}

func TestConfiguredRequestDeadline(t *testing.T) {
	handler := New(expense.NewService(deadlineRepo{&fakeRepo{}}), 10*time.Millisecond)
	started := time.Now()
	response := request(handler, http.MethodGet, "/api/expenses", "", "", "")
	if response.Code != http.StatusInternalServerError || time.Since(started) > time.Second {
		t.Fatal("configured request deadline was not applied")
	}
}

type failedWriter struct{ header http.Header }

func (writer *failedWriter) Header() http.Header {
	return writer.header
}

func (writer *failedWriter) WriteHeader(int) {
}

func (writer *failedWriter) Write([]byte) (int, error) {
	return 0, errors.New("private-write-error")
}

func TestResponseWriteFailureLogging(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
	})
	handler := New(expense.NewService(&fakeRepo{}), time.Second)
	handler.ServeHTTP(&failedWriter{header: make(http.Header)}, httptest.NewRequest("GET", "/api/expenses", nil))
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["level"] != "ERROR" || record["error_category"] != "response_write" || strings.Contains(output.String(), "private-") {
		t.Fatal("response write failure was hidden or leaked private data")
	}
}
