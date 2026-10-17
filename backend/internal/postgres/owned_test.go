package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOwnerReadsIntegration(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repository := New(pool)
	admin := expense.NewService(repository)
	service := expense.NewOwnerService(repository)
	inputs := []expense.Input{
		{UserID: "101", Amount: "0.10", Category: "еда", SpentAt: "2026-10-15"},
		{UserID: "202", Amount: "900", Category: "еда", SpentAt: "2026-10-16"},
		{UserID: "101", Amount: "0.20", Category: "еда", SpentAt: "2026-10-16"},
		{UserID: "101", Amount: "2", Category: "транспорт", SpentAt: "2024-02-29"},
	}
	items := make([]expense.Expense, 0, len(inputs))
	for _, input := range inputs {
		item, err := admin.Create(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, item)
	}
	for _, pair := range [][2]int64{{101, items[1].ID}, {202, items[0].ID}, {101, 9223372036854775807}} {
		if _, err := service.GetByID(ctx, pair[0], pair[1]); !errors.Is(err, expense.ErrNotFound) {
			t.Fatalf("foreign or missing expense readable: %v", err)
		}
	}
	got, err := service.GetByID(ctx, 101, items[0].ID)
	if err != nil || !reflect.DeepEqual(got, items[0]) {
		t.Fatalf("own expense retrieval: %v", err)
	}

	tests := []struct {
		name   string
		owner  int64
		filter expense.OwnerFilter
		ids    []int64
		total  string
	}{
		{"A", 101, expense.OwnerFilter{}, []int64{items[2].ID, items[0].ID, items[3].ID}, "2.30"},
		{"B", 202, expense.OwnerFilter{}, []int64{items[1].ID}, "900.00"},
		{"empty", 303, expense.OwnerFilter{}, []int64{}, "0.00"},
		{"category", 101, expense.OwnerFilter{Category: expense.CategoryFood}, []int64{items[2].ID, items[0].ID}, "0.30"},
		{"inclusive range", 101, expense.OwnerFilter{DateFrom: "2026-10-15", DateTo: "2026-10-16"}, []int64{items[2].ID, items[0].ID}, "0.30"},
		{"lower only", 101, expense.OwnerFilter{DateFrom: "2026-10-16"}, []int64{items[2].ID}, "0.20"},
		{"upper only", 101, expense.OwnerFilter{DateTo: "2024-02-29"}, []int64{items[3].ID}, "2.00"},
		{"AND", 101, expense.OwnerFilter{DateFrom: "2026-10-16", DateTo: "2026-10-16", Category: expense.CategoryFood}, []int64{items[2].ID}, "0.20"},
		{"no matches", 202, expense.OwnerFilter{Category: expense.CategoryTransport}, []int64{}, "0.00"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, err := service.List(ctx, tc.owner, tc.filter, 50, 0)
			if err != nil {
				t.Fatal(err)
			}
			if page.Items == nil || len(page.Items) != len(tc.ids) || page.Total != int64(len(tc.ids)) {
				t.Fatalf("unexpected list size: %+v", page)
			}
			for i, item := range page.Items {
				if item.UserID != tc.owner || item.ID != tc.ids[i] {
					t.Fatal("list disclosed another owner or changed ordering")
				}
			}
			total, err := service.Total(ctx, tc.owner, tc.filter)
			if err != nil || total != tc.total {
				t.Fatalf("want total %s, got %s, %v", tc.total, total, err)
			}
		})
	}
	for _, offset := range []int{1, 100} {
		page, err := service.List(ctx, 101, expense.OwnerFilter{}, 1, offset)
		if err != nil || page.Total != 3 || page.Limit != 1 || page.Offset != offset {
			t.Fatalf("owner pagination: %+v, %v", page, err)
		}
		if offset == 1 && (len(page.Items) != 1 || page.Items[0].ID != items[0].ID) {
			t.Fatal("incorrect middle page")
		}
		if offset == 100 && (page.Items == nil || len(page.Items) != 0) {
			t.Fatal("incorrect empty page")
		}
	}

	// Equal creation instants exercise the id tie-breaker. A later foreign row
	// and a newer purchase date must not influence A's latest selection.
	if _, err := pool.Exec(ctx, `UPDATE expenses SET created_at = '2026-10-16T09:00:00Z' WHERE user_id = 101`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE expenses SET created_at = '2026-10-17T09:00:00Z' WHERE user_id = 202`); err != nil {
		t.Fatal(err)
	}
	latest, err := service.Latest(ctx, 101)
	if err != nil || latest.ID != items[3].ID {
		t.Fatalf("latest creation tie: %+v, %v", latest, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE expenses SET created_at = '2026-10-16T10:00:00Z' WHERE id = $1`, items[0].ID); err != nil {
		t.Fatal(err)
	}
	latest, err = service.Latest(ctx, 101)
	if err != nil || latest.ID != items[0].ID {
		t.Fatalf("created_at must precede id in ordering: %+v, %v", latest, err)
	}
	latest, err = service.Latest(ctx, 202)
	if err != nil || latest.ID != items[1].ID {
		t.Fatalf("B latest: %+v, %v", latest, err)
	}
	if _, err := service.Latest(ctx, 303); !errors.Is(err, expense.ErrNotFound) {
		t.Fatalf("empty owner's latest: %v", err)
	}
	// The repository must never interpret owner zero as an administrative query.
	if _, err := repository.ListOwned(ctx, 0, expense.OwnerFilter{}, 50, 0); !errors.Is(err, expense.ErrInvalidID) {
		t.Fatalf("unscoped list accepted: %v", err)
	}
	if _, err := repository.TotalOwned(ctx, 0, expense.OwnerFilter{}); !errors.Is(err, expense.ErrInvalidID) {
		t.Fatalf("unscoped total accepted: %v", err)
	}
}

func TestOwnerTotalBeyondInt64Integration(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const insert = `
		INSERT INTO expenses (user_id, amount, category, spent_at)
		SELECT 101, 999999999999.99, 'еда', DATE '2026-10-17'
		FROM generate_series(1, 100000)`
	if _, err := pool.Exec(ctx, insert); err != nil {
		t.Fatal(err)
	}
	total, err := expense.NewOwnerService(New(pool)).Total(ctx, 101, expense.OwnerFilter{})
	if err != nil || total != "99999999999999000.00" {
		t.Fatalf("aggregate exceeds int64 hundredths: %s, %v", total, err)
	}
}

func TestOwnerMutationsIntegration(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repository := New(pool)
	admin := expense.NewService(repository)
	service := expense.NewOwnerService(repository)
	first := createTestExpense(t, ctx, admin, "101")
	second := createTestExpense(t, ctx, admin, "202")
	amount := "450"
	for _, pair := range [][2]expense.Expense{{first, second}, {second, first}} {
		owner, target := pair[0].UserID, pair[1]
		_, err := service.Update(ctx, owner, target.ID, target.UpdatedAt, expense.OwnerUpdateInput{Amount: &amount})
		if !errors.Is(err, expense.ErrNotFound) {
			t.Fatalf("foreign update: %v", err)
		}
		if err := service.Delete(ctx, owner, target.ID, target.UpdatedAt); !errors.Is(err, expense.ErrNotFound) {
			t.Fatalf("foreign delete: %v", err)
		}
		stored, err := repository.GetByID(ctx, target.ID)
		if err != nil || !reflect.DeepEqual(stored, target) {
			t.Fatal("foreign operation changed the row")
		}
	}
	category := "транспорт"
	date := "2024-02-29"
	updated, err := service.Update(ctx, 101, first.ID, first.UpdatedAt, expense.OwnerUpdateInput{
		Amount: &amount, Category: &category, SpentAt: &date, CommentSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.UserID != 101 || updated.Amount != 45000 || updated.Category != expense.CategoryTransport || updated.SpentAt != expense.Date(date) || updated.Comment != nil {
		t.Fatal("owner update lost fields")
	}
	if updated.ID != first.ID || !updated.CreatedAt.Equal(first.CreatedAt) || !updated.UpdatedAt.After(first.UpdatedAt) {
		t.Fatal("owner update changed identity or failed to advance version")
	}
	_, err = service.Update(ctx, 101, first.ID, first.UpdatedAt, expense.OwnerUpdateInput{Amount: &amount})
	if !errors.Is(err, expense.ErrNotFound) {
		t.Fatalf("stale update: %v", err)
	}
	if err := service.Delete(ctx, 101, first.ID, first.UpdatedAt); !errors.Is(err, expense.ErrNotFound) {
		t.Fatalf("stale delete: %v", err)
	}
	badDate := "2026-02-29"
	_, err = service.Update(ctx, 101, first.ID, updated.UpdatedAt, expense.OwnerUpdateInput{Amount: &amount, SpentAt: &badDate})
	var fields expense.ValidationError
	if !errors.As(err, &fields) {
		t.Fatalf("invalid mutation: %v", err)
	}
	stored, err := repository.GetByID(ctx, first.ID)
	if err != nil || !reflect.DeepEqual(stored, updated) {
		t.Fatal("stale or invalid operation changed the row")
	}
	if err := service.Delete(ctx, 101, first.ID, updated.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, 101, first.ID, updated.UpdatedAt); !errors.Is(err, expense.ErrNotFound) {
		t.Fatalf("repeat delete: %v", err)
	}
	_, err = service.Update(ctx, 101, first.ID, updated.UpdatedAt, expense.OwnerUpdateInput{Amount: &amount})
	if !errors.Is(err, expense.ErrNotFound) {
		t.Fatalf("update deleted row: %v", err)
	}
	stored, err = repository.GetByID(ctx, second.ID)
	if err != nil || !reflect.DeepEqual(stored, second) {
		t.Fatal("B changed during A's operations")
	}
}

func TestPanelChangesInvalidateOwnerSelectionIntegration(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repository := New(pool)
	admin := expense.NewService(repository)
	service := expense.NewOwnerService(repository)
	amount := "450"
	newOwner := "202"
	for _, operation := range []string{"edit", "reassign", "delete"} {
		t.Run(operation, func(t *testing.T) {
			selected := createTestExpense(t, ctx, admin, "101")
			var err error
			switch operation {
			case "edit":
				_, err = admin.Update(ctx, selected.ID, expense.UpdateInput{Amount: &amount})
			case "reassign":
				_, err = admin.Update(ctx, selected.ID, expense.UpdateInput{UserID: &newOwner})
			case "delete":
				err = admin.Delete(ctx, selected.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Update(ctx, 101, selected.ID, selected.UpdatedAt, expense.OwnerUpdateInput{Amount: &amount})
			if !errors.Is(err, expense.ErrNotFound) {
				t.Fatalf("stale panel selection updated: %v", err)
			}
			if err := service.Delete(ctx, 101, selected.ID, selected.UpdatedAt); !errors.Is(err, expense.ErrNotFound) {
				t.Fatalf("stale panel selection deleted: %v", err)
			}
			if operation == "reassign" {
				// Even knowing the current version cannot bypass the owner guard.
				current, err := service.GetByID(ctx, 202, selected.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err := service.Delete(ctx, 101, current.ID, current.UpdatedAt); !errors.Is(err, expense.ErrNotFound) {
					t.Fatalf("reassigned row deleted by old owner: %v", err)
				}
			}
		})
	}
}

func TestExpenseVersionsAdvanceIntegration(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repository := New(pool)
	admin := expense.NewService(repository)
	service := expense.NewOwnerService(repository)
	selected := createTestExpense(t, ctx, admin, "101")
	// Simulate a clock correction: a stored version is ahead of the wall clock.
	future := selected.UpdatedAt.Add(time.Hour)
	if _, err := pool.Exec(ctx, `UPDATE expenses SET updated_at = $2 WHERE id = $1`, selected.ID, future); err != nil {
		t.Fatal(err)
	}
	amount := "450"
	panelUpdated, err := admin.Update(ctx, selected.ID, expense.UpdateInput{Amount: &amount})
	if err != nil || !panelUpdated.UpdatedAt.After(future) {
		t.Fatalf("panel failed to advance the stored version: %v", err)
	}
	updated, err := service.Update(ctx, 101, selected.ID, panelUpdated.UpdatedAt, expense.OwnerUpdateInput{Amount: &amount})
	if err != nil || !updated.UpdatedAt.After(panelUpdated.UpdatedAt) {
		t.Fatalf("owner update failed to advance the stored version: %v", err)
	}
	if err := service.Delete(ctx, 101, selected.ID, panelUpdated.UpdatedAt); !errors.Is(err, expense.ErrNotFound) {
		t.Fatalf("an old version remained usable: %v", err)
	}
}

func TestConcurrentOwnerMutationsIntegration(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repository := New(pool)
	admin := expense.NewService(repository)
	service := expense.NewOwnerService(repository)
	for _, scenario := range []string{"updates", "update and delete", "deletes"} {
		t.Run(scenario, func(t *testing.T) {
			selected := createTestExpense(t, ctx, admin, "101")
			start := make(chan struct{})
			results := make(chan error, 2)
			for index := range 2 {
				go func() {
					<-start
					if scenario == "deletes" || (scenario == "update and delete" && index == 1) {
						results <- service.Delete(ctx, 101, selected.ID, selected.UpdatedAt)
						return
					}
					amount := "450"
					_, err := service.Update(ctx, 101, selected.ID, selected.UpdatedAt, expense.OwnerUpdateInput{Amount: &amount})
					results <- err
				}()
			}
			close(start)
			successes, rejected := 0, 0
			for range 2 {
				err := <-results
				switch {
				case err == nil:
					successes++
				case errors.Is(err, expense.ErrNotFound):
					rejected++
				default:
					t.Fatal(err)
				}
			}
			if successes != 1 || rejected != 1 {
				t.Fatalf("expected one write and one stale rejection, got %d/%d", successes, rejected)
			}
			stored, err := repository.GetByID(ctx, selected.ID)
			if scenario == "updates" && (err != nil || stored.Amount != 45000 || stored.UserID != 101) {
				t.Fatalf("concurrent update persisted incorrect state: %+v, %v", stored, err)
			}
			if scenario == "deletes" && !errors.Is(err, expense.ErrNotFound) {
				t.Fatalf("concurrent deletion did not persist: %v", err)
			}
			if scenario == "update and delete" && err != nil && !errors.Is(err, expense.ErrNotFound) {
				t.Fatalf("concurrent mutation read failed: %v", err)
			}
			if scenario == "update and delete" && err == nil && (stored.Amount != 45000 || stored.UserID != 101) {
				t.Fatal("concurrent update/delete persisted incorrect state")
			}
		})
	}
}

func waitForBlockedWriter(t *testing.T, ctx context.Context, pool *pgxpool.Pool, blockerPID uint32) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		const query = `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity WHERE $1::integer = ANY(pg_blocking_pids(pid))
		)`
		if err := pool.QueryRow(ctx, query, int64(blockerPID)).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("owner mutation did not wait for the panel transaction")
		case <-ticker.C:
		}
	}
}

func TestOwnerGuardsAfterConcurrentPanelWriteIntegration(t *testing.T) {
	pool := testPool(t)
	repository := New(pool)
	admin := expense.NewService(repository)
	service := expense.NewOwnerService(repository)
	for _, panelChange := range []string{"edit", "reassign"} {
		for _, mutation := range []string{"update", "delete"} {
			t.Run(panelChange+"/"+mutation, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				selected := createTestExpense(t, ctx, admin, "101")
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					cleanupCtx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
					defer cancel()
					// Rollback releases the test lock on failure; after commit it is a no-op.
					_ = tx.Rollback(cleanupCtx)
				}()
				newOwner := int64(101)
				if panelChange == "reassign" {
					newOwner = 202
				}
				// Reassignment deliberately preserves updated_at to prove the owner
				// predicate itself is rechecked after waiting on the row lock.
				const panelWrite = `
					UPDATE expenses SET user_id = $2, amount = 500,
					updated_at = CASE WHEN $2::bigint = 101 THEN updated_at + interval '1 second' ELSE updated_at END
					WHERE id = $1`
				if _, err := tx.Exec(ctx, panelWrite, selected.ID, newOwner); err != nil {
					t.Fatal(err)
				}
				result := make(chan error, 1)
				go func() {
					if mutation == "delete" {
						result <- service.Delete(ctx, 101, selected.ID, selected.UpdatedAt)
						return
					}
					amount := "450"
					_, err := service.Update(ctx, 101, selected.ID, selected.UpdatedAt, expense.OwnerUpdateInput{Amount: &amount})
					result <- err
				}()
				waitForBlockedWriter(t, ctx, pool, tx.Conn().PgConn().PID())
				if err := tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				if err := <-result; !errors.Is(err, expense.ErrNotFound) {
					t.Fatalf("guard did not reject the concurrent panel change: %v", err)
				}
				stored, err := repository.GetByID(ctx, selected.ID)
				if err != nil || stored.UserID != newOwner || stored.Amount != 50000 {
					t.Fatalf("owner mutation changed panel result: %+v, %v", stored, err)
				}
			})
		}
	}
}

func TestOwnedCommentSearchIntegration(t *testing.T) {
	pool := testPool(t)
	repository := New(pool)
	service := expense.NewService(repository)
	ctx := context.Background()
	for _, owner := range []string{"101", "202"} {
		for _, comment := range []string{"Такси 100%_", "автобус"} {
			if _, err := service.Create(ctx, expense.Input{UserID: owner, Amount: "350", Category: "транспорт", SpentAt: "2026-10-17", Comment: &comment}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, query := range []string{"такси", "%_"} {
		filter := expense.OwnerFilter{Query: query}
		page, err := repository.ListOwned(ctx, 101, filter, 10, 0)
		if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].UserID != 101 {
			t.Fatalf("literal owner search: %+v %v", page, err)
		}
		total, err := repository.TotalOwned(ctx, 101, filter)
		if err != nil || total != "350.00" {
			t.Fatalf("total=%s error=%v", total, err)
		}
	}
}
