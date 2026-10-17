package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type loggingAPI struct {
	updates func(context.Context, int64) ([]Update, error)
	send    func(context.Context, int64, string) error
}

func (api loggingAPI) Updates(ctx context.Context, offset int64) ([]Update, error) {
	return api.updates(ctx, offset)
}

func (api loggingAPI) Send(ctx context.Context, chatID int64, text string) error {
	return api.send(ctx, chatID, text)
}

func captureBotLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() {
		slog.SetDefault(previous)
	})
	return &output
}

func decodeBotLogs(t *testing.T, output *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["component"] != "telegram" {
			continue
		}
		records = append(records, record)
	}
	return records
}

func TestProcessLogging(t *testing.T) {
	// Even dependency errors are untrusted: they may contain payloads or URLs with tokens.
	privateError := errors.New("private-expense private-token https://example.invalid/bot-secret")
	cases := []struct {
		name       string
		owner      int64
		handlerErr error
		sendErr    error
		wantErr    error
		messages   []string
	}{
		{name: "allowed A", owner: 101, messages: []string{"Telegram update processing started", "Telegram update processing completed", "Telegram response delivered"}},
		{name: "allowed B", owner: 202, messages: []string{"Telegram update processing started", "Telegram update processing completed", "Telegram response delivered"}},
		{name: "denied C", owner: 303, messages: []string{"Telegram access denied"}},
		{name: "denial delivery failure", owner: 303, sendErr: privateError, wantErr: privateError, messages: []string{"Telegram access denied", "Telegram denial delivery failed"}},
		{name: "processing failure", owner: 101, handlerErr: privateError, wantErr: privateError, messages: []string{"Telegram update processing started", "Telegram update processing failed"}},
		{name: "response delivery failure", owner: 101, sendErr: privateError, wantErr: privateError, messages: []string{"Telegram update processing started", "Telegram update processing completed", "Telegram response delivery failed"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			output := captureBotLogs(t)
			var handled, sent int
			api := loggingAPI{send: func(_ context.Context, chatID int64, text string) error {
				sent++
				if chatID != test.owner {
					t.Fatal("wrong recipient")
				}
				if test.owner == 303 && text != Denied {
					t.Fatal("missing denial")
				}
				return test.sendErr
			}}
			bot := NewBot(api, map[int64]bool{101: true, 202: true}, func(_ context.Context, updateID, owner int64, text string, date time.Time) (string, error) {
				handled++
				if updateID != 7 || owner != test.owner || text != "private-message" || date.Unix() != 100 {
					t.Fatal("handler input changed")
				}
				return "private-response", test.handlerErr
			})
			update := Update{ID: 7, Message: &Message{From: &User{ID: test.owner}, Chat: Chat{ID: test.owner, Type: "private"}, Date: 100, Text: "private-message"}}
			if err := bot.Process(context.Background(), update); !errors.Is(err, test.wantErr) {
				t.Fatalf("unexpected error: %v", err)
			}
			if test.owner == 303 && handled != 0 || test.owner != 303 && handled != 1 {
				t.Fatal("authorization changed")
			}
			if test.handlerErr != nil && sent != 0 || test.handlerErr == nil && sent != 1 {
				t.Fatal("delivery behavior changed")
			}
			records := decodeBotLogs(t, output)
			if len(records) != len(test.messages) {
				t.Fatalf("unexpected records: %s", output)
			}
			for index, record := range records {
				if record["msg"] != test.messages[index] || record["update_id"] != float64(7) || record["user_id"] != float64(test.owner) {
					t.Fatalf("unexpected log record: %v", record)
				}
				if strings.HasSuffix(test.messages[index], "failed") && record["level"] != "ERROR" {
					t.Fatal("failure not logged as error")
				}
				if strings.HasSuffix(test.messages[index], "completed") && record["duration_ms"] == nil {
					t.Fatal("processing duration missing")
				}
			}
			for _, private := range []string{"private-message", "private-response", "private-expense", "private-token", "bot-secret", "example.invalid"} {
				if strings.Contains(output.String(), private) {
					t.Fatal("sensitive data leaked into logs")
				}
			}
		})
	}
}

func TestIgnoredUpdateLogging(t *testing.T) {
	output := captureBotLogs(t)
	// Nil dependencies also ensure ignored updates cannot call a handler or API.
	bot := NewBot(nil, nil, nil)
	updates := []Update{
		{ID: 1},
		{ID: 2, Message: &Message{From: &User{ID: 101}, Chat: Chat{ID: 101, Type: "private-untrusted"}}},
		{ID: 3, Message: &Message{From: &User{ID: 101}, Chat: Chat{ID: 202, Type: "private"}, Text: "private-message"}},
	}
	for _, update := range updates {
		if err := bot.Process(context.Background(), update); err != nil {
			t.Fatal(err)
		}
	}
	records := decodeBotLogs(t, output)
	if len(records) != len(updates) {
		t.Fatal("missing ignored update logs")
	}
	for _, record := range records {
		if record["msg"] != "Telegram update ignored" || record["level"] != "DEBUG" || record["reason"] == nil {
			t.Fatalf("unexpected ignored update record: %v", record)
		}
	}
	if strings.Contains(output.String(), "private-untrusted") || strings.Contains(output.String(), "private-message") {
		t.Fatal("untrusted message fields leaked")
	}
}

func TestPollingRetryLoggingPreservesOffset(t *testing.T) {
	output := captureBotLogs(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	privateError := errors.New("private-polling-error")
	var polls, handled int
	expectedOffsets := []int64{0, 0, 0, 8}
	api := loggingAPI{
		updates: func(_ context.Context, offset int64) ([]Update, error) {
			if polls >= len(expectedOffsets) || offset != expectedOffsets[polls] {
				t.Fatalf("unexpected offset %d at poll %d", offset, polls)
			}
			polls++
			if polls == 1 {
				return nil, privateError
			}
			if polls == 4 {
				cancel()
				return nil, context.Canceled
			}
			return []Update{{ID: -1}, {ID: 7, Message: &Message{From: &User{ID: 101}, Chat: Chat{ID: 101, Type: "private"}, Date: 100, Text: "private-message"}}}, nil
		},
		send: func(context.Context, int64, string) error {
			return nil
		},
	}
	bot := NewBot(api, map[int64]bool{101: true}, func(context.Context, int64, int64, string, time.Time) (string, error) {
		handled++
		if handled == 1 {
			return "", privateError
		}
		return "private-response", nil
	})
	if err := bot.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var pollingFailures, retries, deliveries int
	for _, record := range decodeBotLogs(t, output) {
		switch record["msg"] {
		case "Telegram polling failed":
			pollingFailures++
		case "Telegram polling retry scheduled":
			retries++
			if record["offset"] != float64(0) || record["retry_delay"] == nil {
				t.Fatal("missing retry context")
			}
		case "Telegram response delivered":
			deliveries++
		}
	}
	if polls != 4 || handled != 2 || pollingFailures != 1 || retries != 2 || deliveries != 1 {
		t.Fatalf("unexpected polling logs: %s", output)
	}
	if strings.Contains(output.String(), "private-") {
		t.Fatal("sensitive data leaked into polling logs")
	}
}
