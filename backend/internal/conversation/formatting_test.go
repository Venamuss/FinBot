package conversation

import (
	"context"
	"errors"
	"html"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/botresponse"
	"github.com/Venamuss/FinBot/internal/expense"
)

func TestHTMLRepliesAndLegacyReplay(t *testing.T) {
	orchestrator, model, store := setup(t)
	const text = `кофе </b><script>😀 & чай</script>`
	created := handle(t, orchestrator, model, 1, 101, "synthetic", "create_expense", `{"amount":"6000.01","category":"еда","spent_at":"today","comment":"кофе </b><script>😀 & чай</script>"}`)
	if !strings.Contains(created, html.EscapeString(text)) || !strings.Contains(created, "6 000,01 ₽") || strings.Contains(created, "<script>") {
		t.Fatal(created)
	}
	replayed := handle(t, orchestrator, model, 1, 101, "replay", "delete_expense", `{}`)
	if replayed != created || model.calls != 1 || len(store.repo.items) != 1 || store.results[1] != htmlReplyPrefix+created {
		t.Fatal("formatted reply was not replayed without executing again")
	}
	const legacy = "Добавлено: #7 · 1.00 · еда · 2026-10-18 · <b>чай</b> & кофе"
	store.results[2] = legacy
	store.owners[2] = 101
	replayed = handle(t, orchestrator, model, 2, 101, "legacy", "delete_expense", `{}`)
	if replayed != html.EscapeString(legacy) || model.calls != 1 {
		t.Fatal("legacy user content interpreted as HTML:", replayed)
	}
	reads := store.reads
	if denied := handle(t, orchestrator, model, 2, 303, "legacy", "list_expenses", `{}`); denied != Denied || store.reads != reads {
		t.Fatal("denied owner read stored result")
	}
}

func TestHTMLPaginationAdvancesByShownRows(t *testing.T) {
	orchestrator, model, store := setup(t)
	for range 20 {
		seedListingExpenses(t, store, 101, "2026-10-17")
	}
	text := strings.Repeat("😀<&>", 500)
	for id, item := range store.repo.items {
		item.Comment = &text
		store.repo.items[id] = item
	}
	var seen []int64
	var pages []string
	offset := 0
	update := int64(1)
	for ; offset < 20; update++ {
		name, args := "navigate_expenses", `{"direction":"next"}`
		if update == 1 {
			name, args = "list_expenses", `{}`
		}
		reply := handle(t, orchestrator, model, update, 101, "synthetic", name, args)
		ids := listingIDs(t, reply)
		if len(ids) < 1 || botresponse.Length(reply) > botresponse.MessageLimit {
			t.Fatal("pagination stalled or message too long")
		}
		offset += len(ids)
		if orchestrator.states[101].listing.NextOffset != int64(offset) {
			t.Fatal("offset skipped undisplayed rows")
		}
		seen = append(seen, ids...)
		pages = append(pages, reply)
	}
	want := make([]int64, 20)
	for index := range want {
		want[index] = int64(20 - index)
	}
	if !slices.Equal(seen, want) {
		t.Fatalf("missing or duplicate rows: %v", seen)
	}
	if len(pages) < 3 {
		t.Fatal("test must exercise multiple message-limited pages")
	}
	for index := len(pages) - 2; index >= 0; index-- {
		reply := handle(t, orchestrator, model, update, 101, "назад", "navigate_expenses", `{"direction":"previous"}`)
		update++
		if reply != pages[index] {
			t.Fatalf("message-limited previous page %d changed: %s", index, reply)
		}
	}
	for index := 1; index < len(pages); index++ {
		reply := handle(t, orchestrator, model, update, 101, "ещё", "navigate_expenses", `{"direction":"next"}`)
		update++
		if reply != pages[index] {
			t.Fatalf("message-limited next page %d changed: %s", index, reply)
		}
	}
}

func TestInvalidAmountReplies(t *testing.T) {
	orchestrator, model, store := setup(t)
	seedListingExpenses(t, store, 101, "2026-10-17")
	for index, operation := range []struct {
		name string
		args string
	}{
		{"create_expense", `{"amount":"1.001","category":"еда","spent_at":"today"}`},
		{"update_expense", `{"amount":"0"}`},
		{"update_expense", `{"amount":"450","previous_amount":"-1"}`},
	} {
		reply := handle(t, orchestrator, model, int64(index+1), 101, "synthetic", operation.name, operation.args)
		if reply != botresponse.InvalidAmount || len(store.repo.items) != 1 || store.repo.items[1].Amount != 100 {
			t.Fatal("invalid amount changed data or gave an unclear response:", reply)
		}
	}
}

type failedModificationRepository struct {
	*memoryRepository
}

func (repo failedModificationRepository) UpdateOwned(context.Context, int64, int64, time.Time, expense.Changes) (expense.Expense, error) {
	return expense.Expense{}, errors.New("private database failure")
}

func (repo failedModificationRepository) DeleteOwned(context.Context, int64, int64, time.Time) error {
	return errors.New("private database failure")
}

func TestFailedModificationsNeverReturnConfirmation(t *testing.T) {
	orchestrator, _, store := setup(t)
	seedListingExpenses(t, store, 101, "2026-10-17")
	service := expense.NewOwnerService(failedModificationRepository{store.repo})
	amount := "450"
	for _, operation := range []action{
		{name: "delete_expense"},
		{name: "update_expense", args: map[string]*string{"amount": &amount}},
	} {
		reply, err := orchestrator.applyModification(context.Background(), service, 101, operation, time.Now(), store.repo.items[1], &state{})
		if err == nil || reply != "" || len(store.repo.items) != 1 {
			t.Fatal("database failure returned success")
		}
	}
}
