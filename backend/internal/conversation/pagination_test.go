package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
)

func TestListingSequentialNavigation(t *testing.T) {
	for _, count := range []int{0, 1, 19, 20, 21, 29, 40, 41, 65} {
		for _, limit := range []int{5, 20} {
			t.Run(fmt.Sprintf("count=%d/limit=%d", count, limit), func(t *testing.T) {
				orchestrator, model, store := setup(t)
				for range count {
					seedListingExpenses(t, store, 101, "2026-10-17")
				}
				updateID := int64(1)
				first := handle(t, orchestrator, model, updateID, 101, "траты", "list_expenses", `{"limit":"`+strconv.Itoa(limit)+`"}`)
				pages := []string{first}
				seen := listingIDs(t, first)
				for len(seen) < count {
					updateID++
					page := handle(t, orchestrator, model, updateID, 101, "ещё", "navigate_expenses", `{"direction":"next"}`)
					ids := listingIDs(t, page)
					if len(ids) == 0 || len(ids) > limit {
						t.Fatal("navigation stalled or exceeded page limit:", page)
					}
					seen = append(seen, ids...)
					pages = append(pages, page)
				}
				want := make([]int64, count)
				for index := range want {
					want[index] = int64(count - index)
				}
				if !slices.Equal(seen, want) {
					t.Fatalf("missing, duplicate or unordered same-date rows: %v", seen)
				}
				lastState := orchestrator.states[101].listing
				for range 2 {
					updateID++
					reply := handle(t, orchestrator, model, updateID, 101, "ещё", "navigate_expenses", `{"direction":"next"}`)
					if reply != "Больше расходов нет." || !reflect.DeepEqual(lastState, orchestrator.states[101].listing) {
						t.Fatal("last-page navigation changed state:", reply)
					}
				}
				for index := len(pages) - 2; index >= 0; index-- {
					updateID++
					reply := handle(t, orchestrator, model, updateID, 101, "назад", "navigate_expenses", `{"direction":"previous"}`)
					if reply != pages[index] {
						t.Fatalf("previous page %d differs: %s", index, reply)
					}
				}
				for range 2 {
					updateID++
					reply := handle(t, orchestrator, model, updateID, 101, "назад", "navigate_expenses", `{"direction":"previous"}`)
					if reply != first || orchestrator.states[101].listing.Offset != 0 {
						t.Fatal("first-page navigation moved below zero:", reply)
					}
				}
				if len(pages) > 1 {
					updateID++
					reply := handle(t, orchestrator, model, updateID, 101, "ещё", "navigate_expenses", `{"direction":"next"}`)
					if reply != pages[1] {
						t.Fatal("next page changed after returning to the first page:", reply)
					}
				}
			})
		}
	}
}

func TestListingNavigationPreservesFiltersAndResetsForNewQuery(t *testing.T) {
	for _, args := range []string{
		`{"period":"this_month","category":"еда","query":"кофе","limit":"5"}`,
		`{"date_from":"2026-10-01","date_to":"2026-10-17","category":"еда","query":"кофе","limit":"5"}`,
	} {
		t.Run(args, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			for range 12 {
				seedListingExpenses(t, store, 101, "2026-10-10")
				item := store.repo.items[store.repo.sequence]
				comment := "кофе"
				item.Comment = &comment
				store.repo.items[item.ID] = item
			}
			seedListingExpenses(t, store, 101, "2024-02-29", "2026-10-17", "2026-10-17")
			item := store.repo.items[store.repo.sequence]
			item.Category = expense.CategoryTransport
			store.repo.items[item.ID] = item
			seedListingExpenses(t, store, 202, "2026-10-10")
			first := handle(t, orchestrator, model, 1, 101, "траты на кофе в этом месяце", "list_expenses", args)
			second := handle(t, orchestrator, model, 2, 101, "ещё", "navigate_expenses", `{"direction":"next"}`)
			if !slices.Equal(listingIDs(t, first), []int64{12, 11, 10, 9, 8}) || !slices.Equal(listingIDs(t, second), []int64{7, 6, 5, 4, 3}) {
				t.Fatal("navigation lost original filters:", first, second)
			}
			// Cross midnight without expiring state: relative ranges remain frozen.
			model.response = tool("navigate_expenses", `{"direction":"previous"}`)
			previous, err := orchestrator.Handle(context.Background(), 3, 101, "назад", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
			if err != nil || previous != first {
				t.Fatalf("navigation changed dates or presentation: %s, %v", previous, err)
			}
			handle(t, orchestrator, model, 4, 101, "ещё", "navigate_expenses", `{"direction":"next"}`)
			reset := handle(t, orchestrator, model, 5, 101, "траты", "list_expenses", `{}`)
			listing := orchestrator.states[101].listing
			if len(listingIDs(t, reset)) != 15 || listing.Offset != 0 || len(listing.PreviousOffsets) != 0 || listing.DateFrom != "" || listing.DateTo != "" || listing.Category != "" || listing.Query != "" || listing.Limit != 20 {
				t.Fatal("new query inherited pagination or filters:", reset)
			}
		})
	}
}

func TestListingNavigationOwnersExpiryAndReplay(t *testing.T) {
	orchestrator, model, store := setup(t)
	now := time.Now()
	orchestrator.now = func() time.Time { return now }
	for range 29 {
		seedListingExpenses(t, store, 101, "2026-10-17")
	}
	for range 21 {
		seedListingExpenses(t, store, 202, "2026-10-16")
	}
	firstA := handle(t, orchestrator, model, 1, 101, "траты", "list_expenses", `{}`)
	firstB := handle(t, orchestrator, model, 2, 202, "траты", "list_expenses", `{}`)
	secondA := handle(t, orchestrator, model, 3, 101, "ещё", "navigate_expenses", `{"direction":"next"}`)
	stateA := orchestrator.states[101].listing
	if replay := handle(t, orchestrator, model, 3, 101, "ещё", "navigate_expenses", `{"direction":"next"}`); replay != secondA || !reflect.DeepEqual(stateA, orchestrator.states[101].listing) {
		t.Fatal("duplicate update advanced navigation")
	}
	if reply := handle(t, orchestrator, model, 4, 202, "назад", "navigate_expenses", `{"direction":"previous"}`); reply != firstB {
		t.Fatal("A's navigation changed B's page:", reply)
	}
	secondB := handle(t, orchestrator, model, 5, 202, "ещё", "navigate_expenses", `{"direction":"next"}`)
	if !slices.Equal(listingIDs(t, secondB), []int64{30}) {
		t.Fatal("B's continuation disclosed A's expenses:", secondB)
	}
	if reply := handle(t, orchestrator, model, 6, 101, "назад", "navigate_expenses", `{"direction":"previous"}`); reply != firstA {
		t.Fatal("B's navigation changed A's page:", reply)
	}
	calls, reads := model.calls, store.reads
	if reply := handle(t, orchestrator, model, 7, 303, "назад", "navigate_expenses", `{"direction":"previous"}`); reply != Denied || model.calls != calls || store.reads != reads {
		t.Fatal("denied user reached model or store")
	}
	now = now.Add(30 * time.Minute)
	if reply := handle(t, orchestrator, model, 8, 101, "назад", "navigate_expenses", `{"direction":"previous"}`); reply != "Сначала запросите список расходов." || orchestrator.states[101].listing != nil || strings.Contains(model.request.Messages[0].Content, `"previous_listing"`) {
		t.Fatal("expired state used for navigation:", reply)
	}
	if reply := handle(t, orchestrator, model, 9, 101, "траты", "list_expenses", `{}`); reply != firstA {
		t.Fatal("new query inherited expired state:", reply)
	}
}

func TestListingRejectsInventedNavigationArguments(t *testing.T) {
	for _, operation := range []struct {
		name string
		args string
	}{
		{"list_expenses", `{"offset":"20"}`},
		{"list_expenses", `{"offset":"0"}`},
		{"navigate_expenses", `{"direction":"next","offset":"40"}`},
		{"navigate_expenses", `{"direction":"previous","category":"еда"}`},
		{"navigate_expenses", `{"direction":"next","limit":"100"}`},
		{"navigate_expenses", `{"direction":"next","user_id":"202"}`},
		{"navigate_expenses", `{"direction":"backward"}`},
		{"navigate_expenses", `{"direction":null}`},
		{"navigate_expenses", `{}`},
	} {
		t.Run(operation.args, func(t *testing.T) {
			orchestrator, model, store := setup(t)
			for range 29 {
				seedListingExpenses(t, store, 101, "2026-10-17")
			}
			handle(t, orchestrator, model, 1, 101, "траты", "list_expenses", `{}`)
			before := orchestrator.states[101].listing
			reply := handle(t, orchestrator, model, 2, 101, "ещё", operation.name, operation.args)
			if reply != invalidRequest || !reflect.DeepEqual(before, orchestrator.states[101].listing) {
				t.Fatal("untrusted navigation changed state:", reply)
			}
		})
	}
	for _, definition := range toolsForModel() {
		if definition.Function.Name != "list_expenses" && definition.Function.Name != "navigate_expenses" {
			continue
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(definition.Function.Parameters, &schema); err != nil {
			t.Fatal(err)
		}
		if _, found := schema.Properties["offset"]; found {
			t.Fatal("model can choose an arbitrary offset")
		}
		if definition.Function.Name == "navigate_expenses" && (len(schema.Properties) != 1 || schema.Properties["direction"] == nil || !definition.Function.Strict) {
			t.Fatal("navigation schema must expose only direction")
		}
	}
}

func TestListingNavigationRespectsOffsetLimit(t *testing.T) {
	orchestrator, model, _ := setup(t)
	before := &listingContext{
		Limit: 20, Offset: 2147483640, NextOffset: 2147483660, Total: 2147483700,
	}
	orchestrator.states[101] = state{listing: before, touched: orchestrator.now()}
	reply := handle(t, orchestrator, model, 1, 101, "ещё", "navigate_expenses", `{"direction":"next"}`)
	if reply != invalidFilter || !reflect.DeepEqual(before, orchestrator.states[101].listing) {
		t.Fatal("navigation exceeded the service offset limit or changed state:", reply)
	}
}

type retryListingStore struct {
	*memoryStore
	failAfterRead bool
}

func (store *retryListingStore) Execute(ctx context.Context, id, owner int64, execute func(context.Context, Repositories) (string, error)) (string, bool, error) {
	// Discard the first attempt, as the PostgreSQL store does on serialization failure.
	if _, err := execute(ctx, store.repo); err != nil {
		return "", false, err
	}
	if store.failAfterRead {
		return "", false, errors.New("synthetic commit failure")
	}
	return store.memoryStore.Execute(ctx, id, owner, execute)
}

func TestListingNavigationTransactionRetries(t *testing.T) {
	orchestrator, model, store := setup(t)
	for range 65 {
		seedListingExpenses(t, store, 101, "2026-10-17")
	}
	first := handle(t, orchestrator, model, 1, 101, "траты", "list_expenses", `{}`)
	second := handle(t, orchestrator, model, 2, 101, "ещё", "navigate_expenses", `{"direction":"next"}`)
	handle(t, orchestrator, model, 3, 101, "ещё", "navigate_expenses", `{"direction":"next"}`)
	// Popping a page leaves spare capacity in history, exercising append aliasing.
	handle(t, orchestrator, model, 4, 101, "назад", "navigate_expenses", `{"direction":"previous"}`)
	before := *orchestrator.states[101].listing
	before.PreviousOffsets = slices.Clone(before.PreviousOffsets)
	retrying := &retryListingStore{memoryStore: store, failAfterRead: true}
	orchestrator.store = retrying
	model.response = tool("navigate_expenses", `{"direction":"next"}`)
	reply, err := orchestrator.Handle(context.Background(), 5, 101, "ещё", time.Now())
	if err == nil || reply != "" || !reflect.DeepEqual(before, *orchestrator.states[101].listing) {
		t.Fatal("failed transaction advanced pagination state")
	}
	retrying.failAfterRead = false
	handle(t, orchestrator, model, 5, 101, "ещё", "navigate_expenses", `{"direction":"next"}`)
	if reply := handle(t, orchestrator, model, 6, 101, "назад", "navigate_expenses", `{"direction":"previous"}`); reply != second {
		t.Fatal("retry duplicated history:", reply)
	}
	if reply := handle(t, orchestrator, model, 7, 101, "назад", "navigate_expenses", `{"direction":"previous"}`); reply != first {
		t.Fatal("retry lost the first page:", reply)
	}
}
