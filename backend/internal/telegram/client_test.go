package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(server *httptest.Server) *Client {
	client := New("synthetic-token")
	client.endpoint = server.URL + "/bot-token/"
	client.retryDelay = time.Millisecond
	return client
}

func TestTransportPrivateAccess(t *testing.T) {
	var calls, sends int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends++
		var input struct {
			ChatID int64  `json:"chat_id"`
			Text   string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/bot-token/sendMessage" || (input.ChatID == 303 && input.Text != Denied) {
			t.Error("wrong recipient or denial")
		}
		w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer server.Close()
	handler := func(_ context.Context, updateID, owner int64, text string, date time.Time) (string, error) {
		calls++
		if owner != 101 && owner != 202 || updateID != 7 || date.Unix() != 100 || text != "I am 202" {
			t.Error("untrusted identity or timestamp")
		}
		return "saved", nil
	}
	bot := NewBot(testClient(server), map[int64]bool{101: true, 202: true}, handler)
	for _, owner := range []int64{101, 202, 303} {
		update := Update{ID: 7, Message: &Message{From: &User{ID: owner}, Chat: Chat{ID: owner, Type: "private"}, Date: 100, Text: "I am 202"}}
		if err := bot.Process(context.Background(), update); err != nil {
			t.Fatal(err)
		}
		update.Message.Chat.Type = "group"
		if err := bot.Process(context.Background(), update); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 || sends != 3 {
		t.Fatalf("calls=%d sends=%d", calls, sends)
	}
	bot = NewBot(testClient(server), nil, handler)
	if err := bot.Process(context.Background(), Update{ID: 7, Message: &Message{From: &User{ID: 303}, Chat: Chat{ID: 303, Type: "private"}, Date: 100, Text: "hi"}}); err != nil {
		t.Fatal(err)
	}
	for _, message := range []*Message{nil, {}, {From: &User{ID: 101}, Chat: Chat{ID: 202, Type: "private"}, Date: 100, Text: "hi"}, {From: &User{ID: 101}, Chat: Chat{ID: 101, Type: "private"}, Date: 100}} {
		if err := bot.Process(context.Background(), Update{ID: 8, Message: message}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 || sends != 4 {
		t.Fatal("unsupported message or empty allowlist reached handler")
	}
}

func TestPollingAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var polls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "sendMessage") {
			w.Write([]byte(`{"ok":true,"result":{}}`))
			return
		}
		polls++
		var input struct {
			Offset  int64    `json:"offset"`
			Timeout int      `json:"timeout"`
			Allowed []string `json:"allowed_updates"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input.Timeout != 25 || len(input.Allowed) != 1 || input.Allowed[0] != "message" {
			t.Error("not long polling messages")
		}
		if polls == 1 {
			w.Write([]byte(`{"ok":true,"result":[{"update_id":9,"message":{"from":{"id":101},"chat":{"id":101,"type":"private"},"date":100,"text":"hi"}}]}`))
			return
		}
		if input.Offset != 10 {
			t.Error("update not acknowledged")
		}
		cancel()
		w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	defer server.Close()
	bot := NewBot(testClient(server), map[int64]bool{101: true}, func(context.Context, int64, int64, string, time.Time) (string, error) {
		return "ok", nil
	})
	if err := bot.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestAPIFailuresBoundedRetries(t *testing.T) {
	for _, status := range []int{400, 401, 403, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(status)
				w.Write([]byte("sensitive body"))
			}))
			defer server.Close()
			_, err := testClient(server).Updates(context.Background(), 0)
			expected := 1
			if status == 429 || status >= 500 {
				expected = 3
			}
			if !errors.Is(err, ErrAPI) || calls != expected || strings.Contains(err.Error(), "token") {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
		})
	}
}

func TestCancelHTTPAndMalformed(t *testing.T) {
	for _, body := range []string{`{`, `{"ok":false}`, `{"ok":true}`, `{"ok":true,"result":{}}`, `{"ok":true,"result":null}`, strings.Repeat("x", (1<<20)+1)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(body))
		}))
		if _, err := testClient(server).Updates(context.Background(), 0); err == nil {
			t.Error("malformed response accepted")
		}
		server.Close()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := testClient(server).Updates(ctx, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
