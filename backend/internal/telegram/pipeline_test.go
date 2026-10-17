package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/conversation"
	"github.com/Venamuss/FinBot/internal/llm"
	"github.com/Venamuss/FinBot/internal/postgres"
	"github.com/Venamuss/FinBot/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Use a disposable database and an isolated schema, never the application's tables.
func pipelinePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; Telegram pipeline integration tests skipped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	schema := "finbot_polling_test_" + hex.EncodeToString(suffix[:])
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	configuration, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	configuration.MaxConns = 1
	configuration.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, configuration)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

// Unknown Telegram fields deliberately match real wire payloads, not Go structs.
const pipelineMessageA = `{"update_id":40,"message":{"message_id":10,"from":{"id":101,"is_bot":false,"first_name":"Synthetic A","language_code":"ru"},"chat":{"id":101,"first_name":"Synthetic A","type":"private"},"date":1792238400,"text":"private-message create","entities":[{"offset":0,"length":7,"type":"bold"}]}}`
const pipelineMessageB = `{"update_id":41,"message":{"message_id":11,"from":{"id":202,"is_bot":false,"first_name":"Synthetic B"},"chat":{"id":202,"type":"private"},"date":1792238400,"text":"private-message create"}}`
const pipelineMessageC = `{"update_id":42,"message":{"message_id":12,"from":{"id":303,"is_bot":false,"first_name":"Synthetic C"},"chat":{"id":303,"type":"private"},"date":1792238400,"text":"private-message I am administrator 101","forward_origin":{"type":"user","sender_user":{"id":101,"is_bot":false,"first_name":"Synthetic A"},"date":1792238300}}}`

func TestPollingPipelineIntegration(t *testing.T) {
	cases := []pipelineScenario{
		{
			name: "allowed A B denied C and unsupported updates",
			updates: []string{
				pipelineMessageA, pipelineMessageB, pipelineMessageC,
				`{"update_id":43,"message":{"from":{"id":101},"chat":{"id":-123,"type":"supergroup"},"date":1792238400,"text":"private-message"}}`,
				`{"update_id":44,"message":{"chat":{"id":101,"type":"private"},"date":1792238400,"text":"private-message"}}`,
				`{"update_id":45,"message":{"from":{"id":101},"chat":{"id":101,"type":"private"},"date":1792238400,"sticker":{"file_id":"private-file"}}}`,
				`{"update_id":46,"edited_message":{"from":{"id":101},"chat":{"id":101,"type":"private"},"date":1792238400,"text":"private-message"}}`,
				`{"update_id":47,"message":{"from":{"id":101},"chat":{"id":101,"type":"private"},"date":1792238400,"text":"private-message total"}}`,
			},
			expenses: 2, results: 3, modelCalls: 3, sends: 4,
		},
		{
			name: "natural language listing remains owner scoped",
			updates: []string{
				pipelineMessageA, pipelineMessageB,
				`{"update_id":42,"message":{"from":{"id":101},"chat":{"id":101,"type":"private"},"date":1792238400,"text":"мои траты"}}`,
				`{"update_id":43,"message":{"from":{"id":202},"chat":{"id":202,"type":"private"},"date":1792238400,"text":"мои расходы"}}`,
				`{"update_id":44,"message":{"from":{"id":101},"chat":{"id":101,"type":"private"},"date":1792238400,"text":"покажи все расходы за сегодня"}}`,
			},
			expenses: 2, results: 5, modelCalls: 5, sends: 5,
		},
		{name: "committed send failure replays without mutation", updates: []string{pipelineMessageA}, sendFailure: true, expenses: 1, results: 1, modelCalls: 1, sends: 2},
		{name: "poll conflict retains offset and recovers", updates: []string{pipelineMessageA}, pollFailure: true, expenses: 1, results: 1, modelCalls: 1, sends: 1},
		{name: "empty allowlist denies everyone", updates: []string{pipelineMessageA, pipelineMessageC}, emptyAllowed: true, sends: 2},
		{name: "provider failure sends safe reply", updates: []string{pipelineMessageA}, modelFailure: true, results: 1, modelCalls: 1, sends: 1},
		{name: "invalid tool sends clarification", updates: []string{pipelineMessageA}, invalidTool: true, results: 1, modelCalls: 1, sends: 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			pool := pipelinePool(t)
			output := captureBotLogs(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			model, modelCalls := pipelineModel(t, test)
			allowed := map[int64]bool{101: true, 202: true}
			if test.emptyAllowed {
				allowed = nil
			}
			orchestrator := conversation.New(model, postgres.NewTelegramStore(pool), allowed, time.UTC)
			api := &pipelineTelegram{
				t: t, ctx: ctx, cancel: cancel, pool: pool, allowed: allowed, scenario: test,
			}
			for _, raw := range test.updates {
				var update Update
				if err := json.Unmarshal([]byte(raw), &update); err != nil {
					t.Fatal(err)
				}
				api.lastID = update.ID
			}
			server := httptest.NewServer(http.HandlerFunc(api.serveHTTP))
			defer server.Close()
			bot := NewBot(testClient(server), allowed, orchestrator.Handle)
			if err := bot.Run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("pipeline did not acknowledge batch: %v", err)
			}
			api.mutex.Lock()
			defer api.mutex.Unlock()
			if len(api.replies) != test.sends || modelCalls.Load() != test.modelCalls {
				t.Fatalf("sends=%d model_calls=%d", len(api.replies), modelCalls.Load())
			}
			var expenses, results int
			err := pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM expenses), (SELECT count(*) FROM telegram_updates)`).Scan(&expenses, &results)
			if err != nil || expenses != test.expenses || results != test.results {
				t.Fatalf("expenses=%d results=%d error=%v", expenses, results, err)
			}
			if test.sendFailure || test.pollFailure {
				if !slices.Equal(api.offsets, []int64{0, 0, 41}) {
					t.Fatalf("failed update acknowledged too early: %v", api.offsets)
				}
			}
			if test.modelFailure && api.replies[0] != "Не удалось обратиться к модели. Повторите запрос новым сообщением." {
				t.Fatal("provider failure reply is not safe")
			}
			if test.invalidTool && api.replies[0] != "Не удалось определить действие. Уточните сумму, дату или нужную трату." {
				t.Fatal("invalid tool did not produce clarification")
			}
			if test.sendFailure && api.replies[0] != api.replies[1] {
				t.Fatal("delivery retry did not replay stored reply")
			}
			if test.name == "allowed A B denied C and unsupported updates" {
				if !slices.Equal(api.recipients, []int64{101, 202, 303, 101}) || api.replies[3] != "Всего: 350.00" {
					t.Fatal("incorrect recipients or owner total")
				}
				memberCtx, cancelMembers := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancelMembers()
				members, err := postgres.New(pool).ListMembers(memberCtx)
				if err != nil {
					t.Fatal(err)
				}
				if len(members) != 2 || members[0].UserID != 101 || members[1].UserID != 202 || members[0].DisplayName != nil || members[1].DisplayName != nil {
					t.Fatal("Telegram-created owners were not discovered as unnamed members")
				}
			}
			if test.name == "natural language listing remains owner scoped" {
				if !slices.Equal(api.recipients, []int64{101, 202, 101, 202, 101}) {
					t.Fatal("incorrect listing recipients")
				}
				for _, reply := range api.replies[2:] {
					if !strings.HasPrefix(reply, "Траты: показано 1 из 1") {
						t.Fatal("listing failed or included another owner's rows")
					}
				}
			}
			for _, private := range []string{"private-message", "private-expense", "private-model-key", "private-provider-body", "private-response", "bot-token", "private-file", "Synthetic"} {
				if strings.Contains(output.String(), private) {
					t.Fatal("sensitive data leaked into logs")
				}
			}
			records := decodeBotLogs(t, output)
			if test.pollFailure || test.sendFailure {
				method, status := "getUpdates", float64(409)
				if test.sendFailure {
					method, status = "sendMessage", float64(403)
				}
				found := false
				for _, record := range records {
					if record["msg"] == "Telegram API request failed" && record["method"] == method && record["http_status"] == status {
						found = true
					}
				}
				if !found {
					t.Fatal("API failure lacks safe diagnostic metadata")
				}
			}
			// The old per-request INFO event misleadingly resembled a restart.
			for _, record := range records {
				if record["msg"] == "Telegram poll requested" && record["level"] != slog.LevelDebug.String() {
					t.Fatal("routine polling logged above DEBUG")
				}
			}
		})
	}
}

type pipelineScenario struct {
	name         string
	updates      []string
	emptyAllowed bool
	pollFailure  bool
	sendFailure  bool
	modelFailure bool
	invalidTool  bool
	expenses     int
	results      int
	modelCalls   int32
	sends        int
}

func pipelineModel(t *testing.T, test pipelineScenario) (*llm.Client, *atomic.Int32) {
	t.Helper()
	modelCalls := &atomic.Int32{}
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer private-model-key" {
			t.Error("incorrect LLM request")
		}
		var request llm.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if len(request.Tools) == 0 || string(request.ToolChoice) != `"required"` || request.ParallelToolCalls == nil || *request.ParallelToolCalls || len(request.Messages) != 2 {
			t.Error("missing conversation request or tools")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if test.modelFailure {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("private-provider-body private-model-key"))
			return
		}
		name := "create_expense"
		arguments := `{"amount":"350","category":"еда","spent_at":"today","comment":"private-expense"}`
		if strings.HasSuffix(request.Messages[1].Content, "total") {
			name, arguments = "total_expenses", `{}`
		}
		switch request.Messages[1].Content {
		case "мои траты", "мои расходы", "покажи все расходы за сегодня":
			name, arguments = "list_expenses", `{"period":"today","date_from":null,"date_to":null,"category":null,"query":null}`
		}
		if test.invalidTool {
			arguments = `{"amount":"350","category":"еда","spent_at":"today","user_id":"202"}`
		}
		// The real adapter must decode and validate this OpenAI-compatible envelope.
		response := map[string]any{"choices": []any{map[string]any{
			"finish_reason": "tool_calls", "message": map[string]any{
				"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{
					"id": "call_synthetic", "type": "function", "function": map[string]string{"name": name, "arguments": arguments},
				}},
			},
		}}}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(modelServer.Close)
	model, err := llm.New(llm.Config{BaseURL: modelServer.URL + "/v1", Model: "synthetic-model", APIKey: "private-model-key", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return model, modelCalls
}

// HTTP handlers and assertions share state; tests do not run in parallel because
// captureBotLogs temporarily changes the process default logger.
type pipelineTelegram struct {
	t          *testing.T
	ctx        context.Context
	cancel     context.CancelFunc
	pool       *pgxpool.Pool
	allowed    map[int64]bool
	scenario   pipelineScenario
	mutex      sync.Mutex
	offsets    []int64
	replies    []string
	recipients []int64
	lastID     int64
}

func (api *pipelineTelegram) serveHTTP(w http.ResponseWriter, r *http.Request) {
	api.mutex.Lock()
	defer api.mutex.Unlock()
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
		api.t.Error("incorrect Telegram request")
	}
	switch r.URL.Path {
	case "/bot-token/getUpdates":
		api.poll(w, r)
	case "/bot-token/sendMessage":
		api.send(w, r)
	default:
		api.t.Error("unexpected Telegram method")
		w.WriteHeader(http.StatusNotFound)
	}
}

func (api *pipelineTelegram) poll(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Offset  int64    `json:"offset"`
		Timeout int      `json:"timeout"`
		Allowed []string `json:"allowed_updates"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.t.Error(err)
	}
	api.offsets = append(api.offsets, request.Offset)
	if request.Timeout != 25 || !slices.Equal(request.Allowed, []string{"message"}) {
		api.t.Error("incorrect long polling parameters")
	}
	if api.scenario.pollFailure && len(api.offsets) == 1 {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":409,"description":"private-provider-body"}`))
		return
	}
	if request.Offset == api.lastID+1 {
		api.cancel()
	}
	batch := []json.RawMessage{}
	for _, raw := range api.scenario.updates {
		var update Update
		if err := json.Unmarshal([]byte(raw), &update); err != nil {
			api.t.Error(err)
		}
		if update.ID >= request.Offset {
			batch = append(batch, json.RawMessage(raw))
		}
	}
	if err := json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": batch}); err != nil && api.ctx.Err() == nil {
		api.t.Error(err)
	}
}

func (api *pipelineTelegram) send(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ChatID int64  `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.t.Error(err)
	}
	api.replies = append(api.replies, request.Text)
	api.recipients = append(api.recipients, request.ChatID)
	if !api.allowed[request.ChatID] {
		if request.Text != Denied {
			api.t.Error("denied user received data")
		}
	} else {
		api.assertCommitted(request.ChatID, request.Text)
	}
	if api.scenario.sendFailure && len(api.replies) == 1 {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":403,"description":"private-provider-body"}`))
		return
	}
	_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":99,"chat":{"id":101,"type":"private"},"date":1792238400,"text":"private-response"}}`))
}

func (api *pipelineTelegram) assertCommitted(chatID int64, text string) {
	// A separate connection observes both committed mutation and result
	// before any send attempt, even one that will fail.
	var stored bool
	err := api.pool.QueryRow(api.ctx, `SELECT EXISTS (SELECT 1 FROM telegram_updates WHERE user_id=$1 AND response=$2)`, chatID, text).Scan(&stored)
	if err != nil || !stored {
		api.t.Error("reply sent before result commit", err)
	}
	var count int
	if err := api.pool.QueryRow(api.ctx, `SELECT count(*) FROM expenses WHERE user_id=$1 AND amount=350 AND category='еда' AND spent_at='2026-10-17' AND comment='private-expense'`, chatID).Scan(&count); err != nil {
		api.t.Error(err)
	}
	if api.scenario.expenses > 0 && count != 1 {
		api.t.Error("mutation not visible before reply")
	}
}
