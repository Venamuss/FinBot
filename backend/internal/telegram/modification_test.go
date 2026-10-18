package telegram

import (
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

	"github.com/Venamuss/FinBot/internal/botresponse"
	"github.com/Venamuss/FinBot/internal/conversation"
	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/internal/llm"
	"github.com/Venamuss/FinBot/internal/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Scripted HTTP APIs exercise the actual tool parser, Telegram adapter, owner
// services and PostgreSQL transactions. They do not test model interpretation.
type modificationWorkflow struct {
	t         *testing.T
	pool      *pgxpool.Pool
	bot       *Bot
	plan      llm.Response
	calls     atomic.Int32
	mutations atomic.Int32
	replies   chan string
	update    Update
}

func newModificationWorkflow(t *testing.T) *modificationWorkflow {
	t.Helper()
	workflow := &modificationWorkflow{t: t, pool: pipelinePool(t), replies: make(chan string, 1)}
	modelAPI := httptest.NewServer(http.HandlerFunc(workflow.complete))
	t.Cleanup(modelAPI.Close)
	model, err := llm.New(llm.Config{BaseURL: modelAPI.URL, Model: "synthetic-model", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	telegramAPI := httptest.NewServer(http.HandlerFunc(workflow.deliver))
	t.Cleanup(telegramAPI.Close)
	allowed := map[int64]bool{101: true, 202: true}
	store := &mutationCountingStore{Store: postgres.NewTelegramStore(workflow.pool), calls: &workflow.mutations}
	orchestrator := conversation.New(model, store, allowed, time.UTC)
	workflow.bot = NewBot(testClient(telegramAPI), allowed, orchestrator.Handle)
	return workflow
}

func (workflow *modificationWorkflow) complete(w http.ResponseWriter, r *http.Request) {
	workflow.calls.Add(1)
	var request llm.Request
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		workflow.t.Error(err)
	}
	available := make(map[string]bool)
	for _, definition := range request.Tools {
		available[definition.Function.Name] = true
	}
	for _, name := range []string{"create_expense", "update_expense", "delete_expense", "list_expenses", "total_expenses"} {
		if !available[name] {
			workflow.t.Error("missing expense tool", name)
		}
	}
	if string(request.ToolChoice) != `"required"` || request.ParallelToolCalls == nil || *request.ParallelToolCalls {
		workflow.t.Error("single tool call not required")
	}
	calls := []any{}
	for index, call := range workflow.plan.ToolCalls {
		calls = append(calls, map[string]any{"id": strconv.Itoa(index + 1), "type": "function", "function": map[string]string{"name": call.Name, "arguments": string(call.Arguments)}})
	}
	response := map[string]any{
		"choices": []any{map[string]any{
			"finish_reason": workflow.plan.FinishReason,
			"message": map[string]any{
				"role": "assistant", "content": "unverified model claim", "tool_calls": calls,
			},
		}},
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		workflow.t.Error(err)
	}
}

func (workflow *modificationWorkflow) deliver(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ChatID int64  `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		workflow.t.Error(err)
	}
	if request.ChatID != 303 && request.Text != botresponse.ProcessingFailure {
		var committed bool
		err := workflow.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM telegram_updates WHERE update_id=$1 AND user_id=$2 AND response=$3)`, workflow.update.ID, request.ChatID, "finbot:html:v1\n"+request.Text).Scan(&committed)
		if err != nil || !committed {
			workflow.t.Error("reply delivered before commit", err)
		}
	}
	workflow.replies <- request.Text
	_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
}

func (workflow *modificationWorkflow) send(owner int64, text, name, args string) string {
	workflow.t.Helper()
	workflow.plan = llm.Response{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{Name: name, Arguments: json.RawMessage(args)}}}
	return workflow.process(owner, text, false)
}

func (workflow *modificationWorkflow) process(owner int64, text string, wantFailure bool) string {
	workflow.t.Helper()
	workflow.update = Update{
		ID: workflow.update.ID + 1,
		Message: &Message{
			From: &User{ID: owner}, Chat: Chat{ID: owner, Type: "private"},
			Date: 1792238400, Text: text,
		},
	}
	err := workflow.bot.Process(context.Background(), workflow.update)
	if (err != nil) != wantFailure {
		workflow.t.Fatalf("unexpected processing failure: %v", err)
	}
	return <-workflow.replies
}

func (workflow *modificationWorkflow) item(id int64) expense.Expense {
	workflow.t.Helper()
	item, err := postgres.New(workflow.pool).GetByID(context.Background(), id)
	if err != nil {
		workflow.t.Fatal(err)
	}
	return item
}

func (workflow *modificationWorkflow) count(owner int64) int {
	workflow.t.Helper()
	var count int
	if err := workflow.pool.QueryRow(context.Background(), `SELECT count(*) FROM expenses WHERE user_id=$1`, owner).Scan(&count); err != nil {
		workflow.t.Fatal(err)
	}
	return count
}

func TestNaturalLanguageModificationRegressionIntegration(t *testing.T) {
	updates := []struct{ text, args, amount string }{
		{"последнее было 450", `{"id":null,"query":null,"amount":"450","previous_amount":null,"category":null,"spent_at":null,"comment":null,"currency":null,"request_context":"new"}`, "450.00"},
		{"последнее было не 350, а 450", `{"amount":"450","previous_amount":"350","request_context":"new"}`, "450.00"},
		{"измени последнюю трату на 500", `{"amount":"500","request_context":"new"}`, "500.00"},
		{"поменяй сумму у такси на 700", `{"id":null,"query":"такси","amount":"700","comment":null,"request_context":"new"}`, "700.00"},
		{"измени расход #15 на 1000 рублей", `{"id":"15","query":null,"amount":"1000","currency":"RUB","request_context":"new"}`, "1000.00"},
	}
	for _, test := range updates {
		t.Run(test.text, func(t *testing.T) {
			workflow := newModificationWorkflow(t)
			if _, err := workflow.pool.Exec(context.Background(), `INSERT INTO expenses(id,user_id,amount,category,spent_at,comment) OVERRIDING SYSTEM VALUE VALUES (15,101,350,'транспорт','2026-10-10','такси'), (16,202,900,'еда','2026-10-16','такси')`); err != nil {
				t.Fatal(err)
			}
			before := workflow.item(15)
			reply := workflow.send(101, test.text, "update_expense", test.args)
			after := workflow.item(15)
			if !strings.Contains(reply, "Расход обновлён") || after.Amount.String() != test.amount {
				t.Fatal("natural language update failed")
			}
			before.Amount = after.Amount
			before.UpdatedAt = after.UpdatedAt
			if !reflect.DeepEqual(before, after) || workflow.item(16).Amount.String() != "900.00" {
				t.Fatal("update changed unrelated data or owner")
			}
		})
	}
	for _, test := range []struct{ text, args string }{
		{"удали такси", `{"id":null,"query":"такси","request_context":"new"}`},
		{"удали последнюю трату", `{"id":null,"query":null,"request_context":"new"}`},
		{"удали расход #15", `{"id":"15","query":null,"request_context":"new"}`},
		{"удали покупку кофе", `{"query":"кофе","request_context":"new"}`},
		{"убери последнюю запись", `{"request_context":"new"}`},
	} {
		t.Run(test.text, func(t *testing.T) {
			workflow := newModificationWorkflow(t)
			if _, err := workflow.pool.Exec(context.Background(), `INSERT INTO expenses(id,user_id,amount,category,spent_at,comment) OVERRIDING SYSTEM VALUE VALUES (15,101,350,'еда','2026-10-10','такси и кофе'), (16,202,900,'еда','2026-10-16','такси и кофе')`); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(workflow.send(101, test.text, "delete_expense", test.args), "Расход удалён") || workflow.count(101) != 0 || workflow.count(202) != 1 {
				t.Fatal("unique own expense was not deleted safely")
			}
		})
	}
}

func TestModificationSelectionIsolationAndReplayIntegration(t *testing.T) {
	workflow := newModificationWorkflow(t)
	workflow.send(101, "такси 350", "create_expense", `{"amount":"350","spent_at":"yesterday","category":"транспорт","comment":"такси"}`)
	workflow.send(101, "такси 500", "create_expense", `{"amount":"500","spent_at":"today","category":"транспорт","comment":"такси"}`)
	workflow.send(202, "кофе 900", "create_expense", `{"amount":"900","spent_at":"today","category":"еда","comment":"кофе"}`)
	// A single selected result becomes focus even when a newer record exists.
	workflow.send(101, "покажи такси за вчера", "list_expenses", `{"period":"yesterday","query":"такси"}`)
	updatedReply := workflow.send(101, "последнее было 450", "update_expense", `{"amount":"450"}`)
	updated := workflow.item(1)
	updateCalls := workflow.calls.Load()
	if err := workflow.bot.Process(context.Background(), workflow.update); err != nil {
		t.Fatal(err)
	}
	if replayed := <-workflow.replies; replayed != updatedReply || workflow.calls.Load() != updateCalls || !reflect.DeepEqual(updated, workflow.item(1)) {
		t.Fatal("duplicate update did not replay the committed result")
	}
	if workflow.item(1).Amount.String() != "450.00" || workflow.item(2).Amount.String() != "500.00" {
		t.Fatal("update ignored conversation focus")
	}
	reply := workflow.send(101, "измени такси на 700", "update_expense", `{"query":"такси","amount":"700"}`)
	if !strings.Contains(reply, "2.") || workflow.item(2).Amount.String() != "500.00" {
		t.Fatal("ambiguous update was not deferred")
	}
	workflow.send(202, "последнее было 950", "update_expense", `{"amount":"950"}`)
	workflow.send(101, "вторая", "choose_expense", `{"choice":"2"}`)
	if workflow.item(1).Amount.String() != "700.00" || workflow.item(3).Amount.String() != "950.00" {
		t.Fatal("selection crossed user state")
	}
	workflow.send(101, "удали такси", "delete_expense", `{"query":"такси"}`)
	workflow.send(101, "отмена", "cancel_request", `{}`)
	if workflow.count(101) != 2 {
		t.Fatal("canceled deletion changed data")
	}
	workflow.send(101, "удали такси", "delete_expense", `{"query":"такси"}`)
	workflow.send(101, "первая", "choose_expense", `{"choice":"1"}`)
	if workflow.count(101) != 1 || workflow.item(1).Amount.String() != "700.00" {
		t.Fatal("delete selection removed wrong record")
	}
	calls := workflow.calls.Load()
	if err := workflow.bot.Process(context.Background(), workflow.update); err != nil {
		t.Fatal(err)
	}
	<-workflow.replies
	if workflow.calls.Load() != calls || workflow.count(101) != 1 {
		t.Fatal("duplicate Telegram update reran mutation or model")
	}
	for _, name := range []string{"update_expense", "delete_expense"} {
		args := `{"id":"3"}`
		if name == "update_expense" {
			args = `{"id":"3","amount":"1"}`
		}
		reply := workflow.send(101, "чужой #3", name, args)
		if strings.Contains(reply, "950") || workflow.item(3).Amount.String() != "950.00" {
			t.Fatal("foreign expense disclosed or changed")
		}
		missingID := workflow.send(101, "неизвестная трата", name, strings.ReplaceAll(args, `"3"`, `"999"`))
		if strings.Contains(missingID, "Расход обновлён") || strings.Contains(missingID, "Расход удалён") {
			t.Fatal("missing expense produced a success reply")
		}
		queryArgs := `{"query":"несуществующая трата","request_context":"new"}`
		if name == "update_expense" {
			queryArgs = `{"query":"несуществующая трата","amount":"450","request_context":"new"}`
		}
		if reply := workflow.send(101, "несуществующая трата", name, queryArgs); reply != "Траты не найдены." {
			t.Fatal("missing description was not handled gracefully")
		}
	}
	calls = workflow.calls.Load()
	if workflow.send(303, "удали всё", "delete_expense", `{}`) != Denied || workflow.calls.Load() != calls {
		t.Fatal("denied user reached model")
	}
	workflow.send(101, "последнее было не 350, а 450", "update_expense", `{"previous_amount":"350","amount":"450","request_context":"new"}`)
	if workflow.item(1).Amount.String() != "700.00" {
		t.Fatal("previous amount mismatch did not defer update")
	}
	// An independent command can replace a pending confirmation without executing it.
	workflow.send(101, "удали расход #1", "delete_expense", `{"id":"1","request_context":"new"}`)
	if workflow.count(101) != 0 {
		t.Fatal("pending confirmation blocked independent deletion")
	}
}

func TestModificationMalformedCallsAndCommitFailureIntegration(t *testing.T) {
	workflow := newModificationWorkflow(t)
	workflow.send(101, "кофе 350", "create_expense", `{"amount":"350","spent_at":"today","comment":"кофе"}`)
	before := workflow.item(1)
	for _, args := range []string{`{"amount":500}`, `{"id":"1","query":"кофе","amount":"450"}`, `{"amount":"1.001"}`, `{"amount":null,"comment":null}`, `{"id":"1","amount":"450","user_id":"202"}`, `{"id":"1","id":"2","amount":"450"}`, `{"id":"1","amount":"450","request_context":"invalid"}`} {
		workflow.send(101, "invalid update", "update_expense", args)
	}
	workflow.plan = llm.Response{FinishReason: "stop"}
	workflow.process(101, "zero calls", false)
	workflow.plan = llm.Response{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{Name: "delete_expense", Arguments: json.RawMessage(`{}`)}, {Name: "delete_expense", Arguments: json.RawMessage(`{}`)}}}
	workflow.process(101, "multiple calls", false)
	if !reflect.DeepEqual(before, workflow.item(1)) {
		t.Fatal("malformed calls changed data")
	}
	// A deferred constraint fails at Commit after the callback and reply formatting.
	_, err := workflow.pool.Exec(context.Background(), `CREATE FUNCTION reject_modification_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic commit failure'; END $$;
 CREATE CONSTRAINT TRIGGER reject_modification_commit AFTER INSERT ON telegram_updates DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_modification_commit()`)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"update_expense", "delete_expense"} {
		args := `{}`
		if name == "update_expense" {
			args = `{"amount":"450"}`
		}
		workflow.plan = llm.Response{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{Name: name, Arguments: json.RawMessage(args)}}}
		reply := workflow.process(101, "failing transaction", true)
		if strings.Contains(reply, "Расход обновлён") || strings.Contains(reply, "Расход удалён") || !reflect.DeepEqual(before, workflow.item(1)) {
			t.Fatal("failed commit confirmed success or changed expense")
		}
	}
	if _, err := workflow.pool.Exec(context.Background(), `DROP TRIGGER reject_modification_commit ON telegram_updates`); err != nil {
		t.Fatal(err)
	}
	// Failed transactions must not advance the in-memory expense version/focus.
	if !strings.Contains(workflow.send(101, "последнее было 450", "update_expense", `{"amount":"450"}`), "Расход обновлён") {
		t.Fatal("failed transaction poisoned focus")
	}
	workflow.send(101, "мои расходы", "list_expenses", `{}`)
	if !strings.Contains(workflow.send(101, "удали кофе", "delete_expense", `{"query":"кофе"}`), "Расход удалён") {
		t.Fatal("sequential deletion failed")
	}
	if strings.Contains(workflow.send(101, "мои расходы", "list_expenses", `{}`), "450 ₽") || workflow.count(101) != 0 {
		t.Fatal("deleted expense remains in listing")
	}
}
