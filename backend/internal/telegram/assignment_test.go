package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/conversation"
	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/internal/httpapi"
	"github.com/Venamuss/FinBot/internal/llm"
	"github.com/Venamuss/FinBot/internal/postgres"
)

// The model is scripted: this verifies the real adapters, orchestration, REST
// and database together, not a provider's ability to interpret Russian.
func TestAssignmentWorkflowIntegration(t *testing.T) {
	pool := pipelinePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}

	plans := map[string]struct{ name, arguments string }{
		"еда 500":          {"create_expense", `{"amount":"500","category":"еда","spent_at":"today"}`},
		"кофе 350":         {"create_expense", `{"amount":"350","category":"еда","spent_at":"today","comment":"кофе"}`},
		"кофе 700":         {"create_expense", `{"amount":"700","category":"еда","spent_at":"today","comment":"кофе"}`},
		"такси 1200 вчера": {"create_expense", `{"amount":"1200","category":"транспорт","spent_at":"yesterday","comment":"такси"}`},
		"последнее было не 350, а 450":            {"update_expense", `{"previous_amount":"350","amount":"450"}`},
		"мои расходы":                             {"list_expenses", `{}`},
		"покажи траты за неделю":                  {"list_expenses", `{"period":"last_seven_days"}`},
		"сколько я потратил на еду в этом месяце": {"total_expenses", `{"period":"this_month","category":"еда"}`},
		"покажи кофе за сегодня":                  {"list_expenses", `{"period":"today","query":"кофе"}`},
		"удали последнюю трату":                   {"delete_expense", `{}`},
	}
	var modelCalls atomic.Int32
	modelAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		var request llm.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) != 2 {
			t.Error("invalid model request", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		plan, ok := plans[request.Messages[1].Content]
		if !ok {
			t.Error("unexpected model request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		response := map[string]any{"choices": []any{map[string]any{
			"finish_reason": "tool_calls",
			"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
				"id": "workflow", "type": "function",
				"function": map[string]string{"name": plan.name, "arguments": plan.arguments},
			}}},
		}}}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	defer modelAPI.Close()
	model, err := llm.New(llm.Config{BaseURL: modelAPI.URL, Model: "synthetic-model", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}

	replies := make(chan string, 1)
	telegramAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Text string `json:"text"`
		}
		if r.URL.Path != "/bot-token/sendMessage" {
			t.Error("unexpected Telegram request")
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		replies <- request.Text
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer telegramAPI.Close()
	allowed := map[int64]bool{101: true, 202: true}
	orchestrator := conversation.New(model, postgres.NewTelegramStore(pool), allowed, location)
	bot := NewBot(testClient(telegramAPI), allowed, orchestrator.Handle)
	// UTC is still October 17; the configured calendar is already October 18.
	reference := time.Date(2026, 10, 17, 21, 30, 0, 0, time.UTC)
	var updateID int64
	send := func(owner int64, text string) string {
		t.Helper()
		updateID++
		update := Update{ID: updateID, Message: &Message{
			From: &User{ID: owner}, Chat: Chat{ID: owner, Type: "private"},
			Date: reference.Unix(), Text: text,
		}}
		if err := bot.Process(ctx, update); err != nil {
			t.Fatal(err)
		}
		return <-replies
	}
	for _, text := range []string{"еда 500", "кофе 350", "последнее было не 350, а 450", "такси 1200 вчера"} {
		reply := send(101, text)
		if !strings.HasPrefix(reply, "Добавлено:") && !strings.HasPrefix(reply, "Изменено:") {
			t.Fatalf("workflow mutation failed: %s", reply)
		}
	}
	if !strings.Contains(send(202, "кофе 700"), "700.00") {
		t.Fatal("second owner's expense was not created")
	}
	repo := postgres.New(pool)
	filter := expense.Filter{Limit: 100, Sort: "id", Order: "asc"}
	beforeReads, err := repo.List(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if beforeReads.Total != 4 || len(beforeReads.Items) != 4 {
		t.Fatal("workflow did not create exactly four expenses")
	}
	for _, check := range []struct{ text, expected string }{
		{"мои расходы", "показано 3 из 3"},
		{"покажи траты за неделю", "показано 3 из 3"},
		{"сколько я потратил на еду в этом месяце", "Всего: 950.00"},
	} {
		reply := send(101, check.text)
		if !strings.Contains(reply, check.expected) || strings.Contains(reply, "700.00") {
			t.Fatalf("incorrect owner-scoped read: %s", reply)
		}
	}
	afterReads, err := repo.List(ctx, filter)
	if err != nil || !reflect.DeepEqual(beforeReads, afterReads) {
		t.Fatal("read-only tools changed expenses", err)
	}
	if beforeReads.Items[0].SpentAt != "2026-10-18" || beforeReads.Items[2].SpentAt != "2026-10-17" {
		t.Fatal("relative dates did not use the message's configured calendar")
	}

	// A web edit must become visible to Telegram without changing ownership.
	coffeeID := beforeReads.Items[1].ID
	admin := httpapi.New(expense.NewService(repo), time.Second)
	request := httptest.NewRequest(http.MethodPatch, "/api/expenses/"+strconv.FormatInt(coffeeID, 10), bytes.NewBufferString(`{"amount":"475"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	admin.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("web edit failed: HTTP %d", response.Code)
	}
	if !strings.Contains(send(101, "покажи кофе за сегодня"), "475.00") {
		t.Fatal("Telegram did not read the web edit")
	}
	if !strings.Contains(send(101, "удали последнюю трату"), "475.00") {
		t.Fatal("Telegram did not delete the focused expense")
	}
	afterDelete, err := repo.List(ctx, filter)
	if err != nil || afterDelete.Total != 3 {
		t.Fatal("incorrect remaining expense count", err)
	}
	for _, item := range afterDelete.Items {
		if item.ID == coffeeID {
			t.Fatal("deleted expense remains")
		}
	}
	if !reflect.DeepEqual(afterDelete.Items[2], beforeReads.Items[3]) {
		t.Fatal("another owner's expense changed")
	}
	callsBeforeDenial := modelCalls.Load()
	if send(303, "кофе 350") != Denied || modelCalls.Load() != callsBeforeDenial {
		t.Fatal("denied user reached the model")
	}
}
