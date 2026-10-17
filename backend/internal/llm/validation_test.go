package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewInvalidConfig(t *testing.T) {
	tests := []Config{
		{},
		{BaseURL: "://synthetic-key", Model: "model", Timeout: time.Second},
		{BaseURL: "http://localhost", Model: "", Timeout: time.Second},
		{BaseURL: "http://localhost", Model: "model", Timeout: 0},
		{BaseURL: "http://localhost", Model: "model", Timeout: -time.Second},
		{BaseURL: "http://localhost", Model: "model", APIKey: "synthetic-key\r\n", Timeout: time.Second},
	}
	for index, configuration := range tests {
		client, err := New(configuration)
		if err == nil || client != nil {
			t.Fatalf("case %d: invalid configuration accepted", index)
		}
		assertSafeError(t, err)
	}
}

func TestInvalidRequestDoesNotSend(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeResponse(t, writer, textResponse)
	}))
	defer server.Close()
	client := testClient(t, server.URL, "", time.Second)
	tests := []Request{
		{},
		{Messages: []Message{{Role: "assistant", Content: "synthetic input"}}},
		{Messages: []Message{{Role: "", Content: "synthetic input"}}},
	}
	for _, choice := range []string{"{", `null`, `1`, `[]`, `"other"`, `{}`, `{"type":"function","function":{}}`} {
		request := textRequest()
		request.ToolChoice = json.RawMessage(choice)
		tests = append(tests, request)
	}
	for _, tool := range []Tool{
		{Type: "other", Function: Function{Name: "record", Parameters: json.RawMessage(`{}`)}},
		{Type: "function", Function: Function{Parameters: json.RawMessage(`{}`)}},
		{Type: "function", Function: Function{Name: "record"}},
		{Type: "function", Function: Function{Name: "record", Parameters: json.RawMessage(`null`)}},
		{Type: "function", Function: Function{Name: "record", Parameters: json.RawMessage(`[]`)}},
		{Type: "function", Function: Function{Name: "record", Parameters: json.RawMessage(`{`)}},
	} {
		request := textRequest()
		request.Tools = []Tool{tool}
		tests = append(tests, request)
	}
	for index, request := range tests {
		_, err := client.Complete(context.Background(), request)
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("case %d: want invalid request, got %v", index, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid request reached endpoint")
	}
}

func TestCanceledBeforeRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeResponse(t, writer, textResponse)
	}))
	defer server.Close()
	client := testClient(t, server.URL, "", time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Complete(ctx, textRequest())
	if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("want cancellation without a request, got %v / %d", err, calls.Load())
	}
}
