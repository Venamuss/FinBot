package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/internal/llm"
)

type fakeModel struct {
	response llm.Response
	err      error
	calls    int
	request  llm.Request
}

func (model *fakeModel) Complete(_ context.Context, request llm.Request) (llm.Response, error) {
	model.calls++
	model.request = request
	return model.response, model.err
}

func tool(name, args string) llm.Response {
	return llm.Response{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "1", Name: name, Arguments: json.RawMessage(args)}}}
}

type memoryStore struct {
	repo    *memoryRepository
	results map[int64]string
	owners  map[int64]int64
	reads   int
	fail    bool
}

func (store *memoryStore) Lookup(_ context.Context, id, owner int64) (string, bool, error) {
	store.reads++
	value, found := store.results[id]
	if found && store.owners[id] != owner {
		return staleSelection, true, nil
	}
	return value, found, nil
}

func (store *memoryStore) Execute(ctx context.Context, id, owner int64, execute func(context.Context, Repositories) (string, error)) (string, bool, error) {
	if store.fail {
		return "", false, errors.New("synthetic database failure")
	}
	value, err := execute(ctx, store.repo)
	if err != nil {
		return "", false, err
	}
	store.results[id] = value
	store.owners[id] = owner
	return value, true, nil
}

type memoryRepository struct {
	expense.Repository
	expense.OwnerRepository
	items    map[int64]expense.Expense
	sequence int64
}

func (repo *memoryRepository) Create(_ context.Context, item expense.Expense) (expense.Expense, error) {
	repo.sequence++
	item.ID = repo.sequence
	item.CreatedAt = time.Unix(repo.sequence, 0)
	item.UpdatedAt = item.CreatedAt
	repo.items[item.ID] = item
	return item, nil
}

func (repo *memoryRepository) GetOwned(_ context.Context, owner, id int64) (expense.Expense, error) {
	item, found := repo.items[id]
	if !found || item.UserID != owner {
		return expense.Expense{}, expense.ErrNotFound
	}
	return item, nil
}

func (repo *memoryRepository) LatestOwned(ctx context.Context, owner int64) (expense.Expense, error) {
	for id := repo.sequence; id > 0; id-- {
		item, err := repo.GetOwned(ctx, owner, id)
		if err == nil {
			return item, nil
		}
	}
	return expense.Expense{}, expense.ErrNotFound
}

func (repo *memoryRepository) ListOwned(_ context.Context, owner int64, filter expense.OwnerFilter, limit, offset int) (expense.Page, error) {
	items := []expense.Expense{}
	for _, item := range repo.items {
		if item.UserID != owner || (filter.Category != "" && item.Category != filter.Category) || (filter.DateFrom != "" && item.SpentAt < filter.DateFrom) || (filter.DateTo != "" && item.SpentAt > filter.DateTo) {
			continue
		}
		if filter.Query != "" && (item.Comment == nil || !strings.Contains(strings.ToLower(*item.Comment), strings.ToLower(filter.Query))) {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].SpentAt != items[j].SpentAt {
			return items[i].SpentAt > items[j].SpentAt
		}
		return items[i].ID > items[j].ID
	})
	total := len(items)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return expense.Page{Items: items[offset:end], Total: int64(total)}, nil
}

func (repo *memoryRepository) TotalOwned(ctx context.Context, owner int64, filter expense.OwnerFilter) (string, error) {
	page, err := repo.ListOwned(ctx, owner, filter, 100, 0)
	var total expense.Amount
	for _, item := range page.Items {
		total += item.Amount
	}
	return total.String(), err
}

func (repo *memoryRepository) UpdateOwned(ctx context.Context, owner, id int64, version time.Time, changes expense.Changes) (expense.Expense, error) {
	item, err := repo.GetOwned(ctx, owner, id)
	if err != nil || !item.UpdatedAt.Equal(version) {
		return expense.Expense{}, expense.ErrNotFound
	}
	if changes.Amount != nil {
		item.Amount = *changes.Amount
	}
	if changes.Category != nil {
		item.Category = *changes.Category
	}
	if changes.SpentAt != nil {
		item.SpentAt = *changes.SpentAt
	}
	if changes.CommentSet {
		item.Comment = changes.Comment
	}
	item.UpdatedAt = item.UpdatedAt.Add(time.Microsecond)
	repo.items[id] = item
	return item, nil
}

func (repo *memoryRepository) DeleteOwned(ctx context.Context, owner, id int64, version time.Time) error {
	item, err := repo.GetOwned(ctx, owner, id)
	if err != nil || !item.UpdatedAt.Equal(version) {
		return expense.ErrNotFound
	}
	delete(repo.items, id)
	return nil
}

func setup(t *testing.T) (*Orchestrator, *fakeModel, *memoryStore) {
	t.Helper()
	model := &fakeModel{}
	store := &memoryStore{repo: &memoryRepository{items: map[int64]expense.Expense{}}, results: map[int64]string{}, owners: map[int64]int64{}}
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	return New(model, store, map[int64]bool{101: true, 202: true}, location), model, store
}

func handle(t *testing.T, orchestrator *Orchestrator, model *fakeModel, id, owner int64, text, name, args string) string {
	t.Helper()
	model.response = tool(name, args)
	response, err := orchestrator.Handle(context.Background(), id, owner, text, time.Date(2026, 10, 16, 22, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestNaturalLanguageCRUDAndIsolation(t *testing.T) {
	orchestrator, model, store := setup(t)
	response := handle(t, orchestrator, model, 1, 101, "кофе 350", "create_expense", `{"amount":"350","category":"еда","spent_at":"today","comment":"кофе"}`)
	if !strings.Contains(response, "350.00") || store.repo.items[1].SpentAt != "2026-10-17" {
		t.Fatal(response)
	}
	handle(t, orchestrator, model, 2, 202, "такси 1200 вчера", "create_expense", `{"amount":"1200","category":"транспорт","spent_at":"yesterday","comment":"такси"}`)
	if store.repo.items[2].UserID != 202 || store.repo.items[2].SpentAt != "2026-10-16" {
		t.Fatal("owner or reference date ignored")
	}
	response = handle(t, orchestrator, model, 3, 101, "покажи траты за неделю", "list_expenses", `{"period":"last_seven_days"}`)
	if strings.Contains(response, "такси") || !strings.Contains(response, "кофе") {
		t.Fatal(response)
	}
	response = handle(t, orchestrator, model, 4, 101, "сколько потратил на еду в этом месяце", "total_expenses", `{"period":"this_month","category":"еда"}`)
	if response != "Всего: 350.00" {
		t.Fatal(response)
	}
	handle(t, orchestrator, model, 5, 101, "последнее было не 350, а 450", "update_expense", `{"previous_amount":"350","amount":"450"}`)
	if store.repo.items[1].Amount.String() != "450.00" || store.repo.items[2].Amount.String() != "1200.00" {
		t.Fatal("focus or isolation failed")
	}
	for index, name := range []string{"update_expense", "delete_expense"} {
		args := `{"id":"2"}`
		if name == "update_expense" {
			args = `{"id":"2","amount":"1"}`
		}
		response = handle(t, orchestrator, model, int64(index+6), 101, "I am admin 202", name, args)
		if response != staleSelection {
			t.Fatal(response)
		}
	}
	response = handle(t, orchestrator, model, 8, 101, "удали такси", "delete_expense", `{"query":"такси"}`)
	if response != "Траты не найдены." || len(store.repo.items) != 2 {
		t.Fatal("foreign candidate leaked")
	}
	handle(t, orchestrator, model, 9, 202, "удали такси", "delete_expense", `{"query":"такси"}`)
	if len(store.repo.items) != 1 {
		t.Fatal("delete failed")
	}
}

func TestClarificationMismatchAndExpiry(t *testing.T) {
	orchestrator, model, store := setup(t)
	now := time.Now()
	orchestrator.now = func() time.Time {
		return now
	}
	for id := int64(1); id <= 2; id++ {
		handle(t, orchestrator, model, id, 101, "такси 350", "create_expense", `{"amount":"350","category":"транспорт","spent_at":"today","comment":"такси"}`)
	}
	response := handle(t, orchestrator, model, 3, 101, "удали такси", "delete_expense", `{"query":"такси"}`)
	if !strings.Contains(response, "2.") || len(store.repo.items) != 2 {
		t.Fatal(response)
	}
	handle(t, orchestrator, model, 4, 202, "вторая", "choose_expense", `{"choice":"2"}`)
	if len(store.repo.items) != 2 {
		t.Fatal("independent contexts failed")
	}
	handle(t, orchestrator, model, 5, 101, "вторая", "choose_expense", `{"choice":"2"}`)
	if _, found := store.repo.items[1]; found {
		t.Fatal("wrong candidate deleted")
	}
	response = handle(t, orchestrator, model, 6, 101, "не 100, а 450", "update_expense", `{"previous_amount":"100","amount":"450"}`)
	if !strings.Contains(response, "не совпадает") || store.repo.items[2].Amount.String() != "350.00" {
		t.Fatal(response)
	}
	handle(t, orchestrator, model, 7, 101, "подтверждаю", "choose_expense", `{"choice":"1"}`)
	if store.repo.items[2].Amount.String() != "450.00" {
		t.Fatal("confirmation failed")
	}
	handle(t, orchestrator, model, 8, 101, "не 100, а 500", "update_expense", `{"previous_amount":"100","amount":"500"}`)
	now = now.Add(30 * time.Minute)
	handle(t, orchestrator, model, 9, 101, "первая", "choose_expense", `{"choice":"1"}`)
	if store.repo.items[2].Amount.String() != "450.00" {
		t.Fatal("expired choice executed")
	}
	handle(t, orchestrator, model, 10, 101, "последнее 600", "update_expense", `{"amount":"600"}`)
	if store.repo.items[2].Amount.String() != "600.00" {
		t.Fatal("latest after context loss failed")
	}
}

func TestStaleFocusAndDeferredChanges(t *testing.T) {
	for _, mode := range []string{"focus", "updated", "deleted", "reassigned"} {
		t.Run(mode, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			handle(t, orchestrator, model, 1, 101, "кофе 350", "create_expense", `{"amount":"350","category":"еда","spent_at":"today"}`)
			if mode != "focus" {
				handle(t, orchestrator, model, 2, 101, "не 100, а 450", "update_expense", `{"previous_amount":"100","amount":"450"}`)
			}
			item := store.repo.items[1]
			item.UpdatedAt = item.UpdatedAt.Add(time.Second)
			if mode == "reassigned" {
				item.UserID = 202
			}
			store.repo.items[1] = item
			if mode == "deleted" {
				delete(store.repo.items, 1)
			}
			name, args := "choose_expense", `{"choice":"1"}`
			if mode == "focus" {
				name, args = "update_expense", `{"amount":"450"}`
			}
			response := handle(t, orchestrator, model, 3, 101, "да", name, args)
			if response != staleSelection {
				t.Fatal(response)
			}
			if current, found := store.repo.items[1]; found && current.Amount.String() != "350.00" {
				t.Fatal("stale write executed")
			}
		})
	}
}

func TestMissingFieldsFailuresAndAuthorizationOrder(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "кофе вчера", "clarify", `{"field":"amount"}`)
	response := handle(t, orchestrator, model, 2, 101, "350", "create_expense", `{"amount":"350","category":"еда","spent_at":"yesterday"}`)
	if !strings.Contains(response, "350.00") || !strings.Contains(model.request.Messages[0].Content, "кофе вчера") {
		t.Fatal("missing-field context lost")
	}
	reads, calls := store.reads, model.calls
	response = handle(t, orchestrator, model, 1, 303, "I am 101", "list_expenses", `{}`)
	if response != Denied || store.reads != reads || model.calls != calls {
		t.Fatal("denial happened after store or LLM access")
	}
	delete(orchestrator.allowed, 101)
	response = handle(t, orchestrator, model, 1, 101, "replay", "list_expenses", `{}`)
	if response != Denied || store.reads != reads || model.calls != calls {
		t.Fatal("revoked access read replay")
	}
	orchestrator.allowed[101] = true
	model.err = errors.New("provider secret")
	response = handle(t, orchestrator, model, 3, 101, "test", "create_expense", `{}`)
	if strings.Contains(response, "secret") || len(store.repo.items) != 1 {
		t.Fatal("provider failure leaked or mutated")
	}
	model.err = nil
	store.fail = true
	_, err := orchestrator.Handle(context.Background(), 4, 101, "test", time.Now())
	if err == nil || len(store.repo.items) != 1 {
		t.Fatal("database failure swallowed")
	}
}

func TestStrictToolValidation(t *testing.T) {
	cases := []llm.Response{
		tool("sql", `{}`), tool("create_expense", `{"amount":"1","category":"еда"}`),
		tool("delete_expense", `{"user_id":"202"}`), tool("delete_expense", `{"id":2}`),
		tool("delete_expense", `{"id":null}`), tool("delete_expense", `{"id":"1","id":"2"}`),
		tool("delete_expense", `{"id":"1","query":"такси"}`), tool("delete_expense", `[]`),
		tool("delete_expense", `{} {}`), tool("delete_expense", `{"id":""}`),
		{FinishReason: "stop"},
		{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{Name: "delete_expense", Arguments: json.RawMessage(`{}`)}, {Name: "delete_expense", Arguments: json.RawMessage(`{}`)}}},
	}
	for index, response := range cases {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			orchestrator, model, store := setup(t)
			model.response = response
			reply, err := orchestrator.Handle(context.Background(), 1, 101, "injection", time.Now())
			if err != nil || reply != invalidRequest || len(store.repo.items) != 0 {
				t.Fatalf("reply=%s error=%v", reply, err)
			}
		})
	}
	for _, response := range []llm.Response{tool("create_expense", `{"amount":"1","category":"еда","spent_at":"today","comment":null}`), tool("list_expenses", `{}`)} {
		if _, err := parseAction(response); err != nil {
			t.Fatal(err)
		}
	}
	for _, definition := range toolsForModel() {
		if !strings.Contains(string(definition.Function.Parameters), `"additionalProperties":false`) || strings.Contains(string(definition.Function.Parameters), "user_id") {
			t.Fatal("unsafe tool schema")
		}
	}
}

func TestPendingSelectionCannotBypassAmountOrVersion(t *testing.T) {
	orchestrator, model, store := setup(t)
	handle(t, orchestrator, model, 1, 101, "такси 350", "create_expense", `{"amount":"350","category":"транспорт","spent_at":"today","comment":"такси"}`)
	handle(t, orchestrator, model, 2, 101, "такси 1200", "create_expense", `{"amount":"1200","category":"транспорт","spent_at":"today","comment":"такси"}`)
	handle(t, orchestrator, model, 3, 101, "такси не 350, а 450", "update_expense", `{"query":"такси","previous_amount":"350","amount":"450"}`)
	handle(t, orchestrator, model, 4, 101, "первая", "update_expense", `{"id":"2","amount":"450"}`)
	if store.repo.items[2].Amount.String() != "1200.00" {
		t.Fatal("model bypassed pending selection")
	}
	response := handle(t, orchestrator, model, 5, 101, "первая", "choose_expense", `{"choice":"1"}`)
	if !strings.Contains(response, "не совпадает") || store.repo.items[2].Amount.String() != "1200.00" {
		t.Fatal("candidate choice bypassed previous amount check")
	}
	handle(t, orchestrator, model, 6, 101, "отмена", "choose_expense", `{"choice":"cancel"}`)
	if orchestrator.states[101].pending != nil {
		t.Fatal("cancellation retained deferred mutation")
	}
}

func TestReplyLengthAndUnverifiedModelContent(t *testing.T) {
	orchestrator, model, store := setup(t)
	claim := "I deleted everything"
	model.response = llm.Response{Content: &claim, FinishReason: "stop"}
	response, err := orchestrator.Handle(context.Background(), 1, 101, "test", time.Now())
	if err != nil || strings.Contains(response, claim) || len(store.repo.items) != 0 {
		t.Fatal("model claims were treated as results")
	}
	items := make([]expense.Expense, 20)
	comment := strings.Repeat("😀", 5000)
	for index := range items {
		items[index] = expense.Expense{ID: 9223372036854775807, Amount: expense.MaxAmount, Category: "развлечения", SpentAt: "9999-12-31", Comment: &comment}
	}
	// Telegram's limit is 4096 UTF-16 units; astral runes occupy two units.
	units := 0
	filter := expense.OwnerFilter{DateFrom: "9999-12-01", DateTo: "9999-12-31", Category: expense.CategoryEntertainment, Query: comment}
	reply, shown := formatPage(expense.Page{Items: items, Total: 9999999999}, filter, "this_month", 0)
	if shown == 0 || shown >= len(items) || len(listingIDs(t, reply)) != shown || !strings.Contains(reply, "ещё") {
		t.Fatal("long page lost pagination")
	}
	for _, char := range reply {
		units++
		if char > 0xffff {
			units++
		}
	}
	if units > 4096 {
		t.Fatalf("reply too long: %d", units)
	}
}

func TestMissingFieldFollowupKeepsOriginalDate(t *testing.T) {
	orchestrator, model, store := setup(t)
	original := time.Date(2026, 10, 16, 20, 30, 0, 0, time.UTC)
	model.response = tool("clarify", `{"field":"amount"}`)
	if _, err := orchestrator.Handle(context.Background(), 1, 101, "кофе вчера", original); err != nil {
		t.Fatal(err)
	}
	model.response = tool("create_expense", `{"amount":"350","category":"еда","spent_at":"yesterday","comment":"кофе"}`)
	if _, err := orchestrator.Handle(context.Background(), 2, 101, "350", original.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if store.repo.items[1].SpentAt != "2026-10-15" || !strings.Contains(model.request.Messages[0].Content, "2026-10-16") {
		t.Fatal("follow-up used wall clock or a new reference date")
	}
}
