package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/jackc/pgx/v5/pgconn"
)

func createTestExpense(t *testing.T, ctx context.Context, service *expense.Service, owner string) expense.Expense {
	t.Helper()
	comment := "synthetic '); DROP TABLE expenses; --"
	item, err := service.Create(ctx, expense.Input{
		UserID: owner, Amount: "350.5", Category: "еда", SpentAt: "2026-10-16", Comment: &comment,
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func assertMembers(t *testing.T, ctx context.Context, repository *Repository, want []int64) {
	t.Helper()
	got, err := repository.ListMembers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(got))
	for _, member := range got {
		ids = append(ids, member.UserID)
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("want members %v, got %v", want, ids)
	}
}

func TestCRUDIntegration(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repository := New(pool)
	service := expense.NewService(repository)
	assertMembers(t, ctx, repository, []int64{})
	first := createTestExpense(t, ctx, service, "10")
	second := createTestExpense(t, ctx, service, "2")
	third := createTestExpense(t, ctx, service, "10")
	assertMembers(t, ctx, repository, []int64{2, 10})

	amount := "450.5"
	updated, err := service.Update(ctx, first.ID, expense.UpdateInput{Amount: &amount})
	if err != nil {
		t.Fatal(err)
	}
	want := first
	want.Amount = 45050
	want.UpdatedAt = updated.UpdatedAt
	if !reflect.DeepEqual(updated, want) || !updated.UpdatedAt.After(first.UpdatedAt) {
		t.Fatal("partial update changed omitted fields or did not advance updated_at")
	}
	stored, err := repository.GetByID(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored, updated) {
		t.Fatal("returned update differs from persisted expense")
	}

	owner := "9223372036854775807"
	category := "транспорт"
	date := "2024-02-29"
	amount = "999999999999.99"
	updated, err = service.Update(ctx, first.ID, expense.UpdateInput{
		UserID: &owner, Amount: &amount, Category: &category, SpentAt: &date, CommentSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.UserID != 9223372036854775807 || updated.Amount != expense.MaxAmount || updated.Category != expense.CategoryTransport || updated.SpentAt != expense.Date(date) || updated.Comment != nil {
		t.Fatal("full update lost fields")
	}
	if updated.ID != first.ID || !updated.CreatedAt.Equal(first.CreatedAt) {
		t.Fatal("update changed immutable fields")
	}
	assertMembers(t, ctx, repository, []int64{2, 10, 9223372036854775807})

	for _, comment := range []string{"", "synthetic '); DROP TABLE expenses; --"} {
		updated, err = service.Update(ctx, first.ID, expense.UpdateInput{CommentSet: true, Comment: &comment})
		if err != nil {
			t.Fatal(err)
		}
		if updated.Comment == nil || *updated.Comment != comment || updated.UserID != 9223372036854775807 || updated.Amount != expense.MaxAmount {
			t.Fatal("comment-only update changed other fields or lost the comment")
		}
	}
	// Reassignment is visible in the same owner filters used by the panel.
	for _, ownerID := range []int64{10, 9223372036854775807} {
		page, err := repository.List(ctx, expense.Filter{UserID: ownerID, Sort: "id", Order: "asc", Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 1 || len(page.Items) != 1 || page.Items[0].UserID != ownerID {
			t.Fatal("owner filter did not reflect reassignment")
		}
	}
	if err := service.Delete(ctx, third.ID); err != nil {
		t.Fatal(err)
	}
	assertMembers(t, ctx, repository, []int64{2, 9223372036854775807})
	if err := service.Delete(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	assertMembers(t, ctx, repository, []int64{2})
	if _, err := repository.GetByID(ctx, first.ID); !errors.Is(err, expense.ErrNotFound) {
		t.Fatalf("deleted expense is still readable: %v", err)
	}
	if err := service.Delete(ctx, first.ID); !errors.Is(err, expense.ErrNotFound) {
		t.Fatalf("repeat delete: %v", err)
	}
	if _, err := service.Update(ctx, first.ID, expense.UpdateInput{Amount: &amount}); !errors.Is(err, expense.ErrNotFound) {
		t.Fatalf("update deleted expense: %v", err)
	}
	if _, err := service.Update(ctx, 9223372036854775807, expense.UpdateInput{Amount: &amount}); !errors.Is(err, expense.ErrNotFound) {
		t.Fatalf("update unknown expense: %v", err)
	}
	if err := service.Delete(ctx, 9223372036854775807); !errors.Is(err, expense.ErrNotFound) {
		t.Fatalf("delete unknown expense: %v", err)
	}
	remaining, err := repository.GetByID(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(remaining, second) {
		t.Fatal("another owner's expense changed")
	}
	if err := service.Delete(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	assertMembers(t, ctx, repository, []int64{})
}

func TestFailedMutationsAreAtomicIntegration(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repository := New(pool)
	service := expense.NewService(repository)
	before := createTestExpense(t, ctx, service, "101")
	amount := "450"
	invalidDate := "2026-02-29"
	_, err := service.Update(ctx, before.ID, expense.UpdateInput{Amount: &amount, SpentAt: &invalidDate, CommentSet: true})
	var fields expense.ValidationError
	if !errors.As(err, &fields) {
		t.Fatalf("expected validation failure, got %v", err)
	}
	after, err := repository.GetByID(ctx, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("validation failure saved part of the update")
	}

	// Bypass service validation to force an actual database constraint failure.
	validAmount := expense.Amount(45000)
	invalidCategory := expense.Category("invalid")
	_, err = repository.Update(ctx, before.ID, expense.Changes{Amount: &validAmount, Category: &invalidCategory, CommentSet: true})
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) || databaseError.Code != "23514" {
		t.Fatalf("expected CHECK failure, got %v", err)
	}
	after, err = repository.GetByID(ctx, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("database failure saved part of the update")
	}

	// This trigger exists only in the test's isolated schema. An AFTER failure
	// verifies PostgreSQL restores the row even after deletion has begun.
	const rejectDelete = `
		CREATE FUNCTION reject_delete() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'synthetic delete failure';
		END;
		$$;
		CREATE TRIGGER reject_delete AFTER DELETE ON expenses
		FOR EACH ROW EXECUTE FUNCTION reject_delete()`
	if _, err := pool.Exec(ctx, rejectDelete); err != nil {
		t.Fatal(err)
	}
	err = repository.Delete(ctx, before.ID)
	if !errors.As(err, &databaseError) || databaseError.Code != "P0001" {
		t.Fatalf("expected delete failure, got %v", err)
	}
	after, err = repository.GetByID(ctx, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("failed delete changed the expense")
	}
	assertMembers(t, ctx, repository, []int64{101})
}

func TestConcurrentMutationsIntegration(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repository := New(pool)
	service := expense.NewService(repository)
	item := createTestExpense(t, ctx, service, "101")
	amount := "450"
	comment := "concurrent synthetic update"
	inputs := []expense.UpdateInput{{Amount: &amount}, {CommentSet: true, Comment: &comment}}
	start := make(chan struct{})
	results := make(chan error, len(inputs))
	for _, input := range inputs {
		go func() {
			<-start
			_, err := service.Update(ctx, item.ID, input)
			results <- err
		}()
	}
	close(start)
	for range inputs {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	updated, err := repository.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Amount != 45000 || updated.Comment == nil || *updated.Comment != comment {
		t.Fatal("concurrent partial update lost another request's change")
	}

	start = make(chan struct{})
	for range 2 {
		go func() {
			<-start
			results <- service.Delete(ctx, item.ID)
		}()
	}
	close(start)
	deleted := 0
	missing := 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			deleted++
		case errors.Is(err, expense.ErrNotFound):
			missing++
		default:
			t.Fatal(err)
		}
	}
	if deleted != 1 || missing != 1 {
		t.Fatalf("concurrent delete results: %d success, %d missing", deleted, missing)
	}
	assertMembers(t, ctx, repository, []int64{})
}
