package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestCategory(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, "none"},
		{context.Canceled, "canceled"},
		{fmt.Errorf("private-context: %w", context.DeadlineExceeded), "timeout"},
		{&net.DNSError{Err: "private-network", IsTimeout: true}, "timeout"},
		{&net.DNSError{Err: "private-network"}, "network"},
		{&pgconn.PgError{Code: "40001", Detail: "private-expense"}, "serialization_failure"},
		{&pgconn.PgError{Code: "40P01", Message: "private-query"}, "deadlock"},
		{&pgconn.PgError{Code: "23514", Detail: "private-row"}, "database"},
		{errors.New("private-password"), "application"},
	}
	for _, test := range cases {
		if got := Category(test.err); got != test.want {
			t.Fatalf("got category %s, want %s", got, test.want)
		}
	}
}

func TestCorrelation(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
	})
	Logger(WithUpdateID(context.Background(), 42), "llm", "complete").Info("completed")
	Logger(WithRequestID(context.Background(), "7"), "http", "request").Info("completed")
	decoder := json.NewDecoder(&output)
	var update, request map[string]any
	if err := decoder.Decode(&update); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&request); err != nil {
		t.Fatal(err)
	}
	if update["update_id"] != float64(42) || update["component"] != "llm" || update["operation"] != "complete" || request["request_id"] != "7" || request["update_id"] != nil {
		t.Fatal("correlation missing or leaked between requests")
	}
}
