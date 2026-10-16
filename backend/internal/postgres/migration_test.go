package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/migrations"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestMigrationUpgradeIntegration(t *testing.T) {
	pool := unmigratedTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	legacySQL, err := os.ReadFile("../../migrations/testdata/legacy_expenses.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(legacySQL)); err != nil {
		t.Fatal(err)
	}

	repository := New(pool)
	input, err := (expense.Input{
		UserID:   "101",
		Amount:   "350.50",
		Category: "еда",
		SpentAt:  "2026-10-16",
	}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	before, err := repository.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := migrations.Apply(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	after, err := repository.GetByID(ctx, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("expense changed during migration: before=%+v after=%+v", before, after)
	}

	var appliedVersions int
	const countAppliedVersions = `
		SELECT count(*)
		FROM goose_db_version
		WHERE version_id = 1 AND is_applied`
	err = pool.QueryRow(ctx, countAppliedVersions).Scan(&appliedVersions)
	if err != nil {
		t.Fatal(err)
	}
	if appliedVersions != 1 {
		t.Fatalf("expected one applied Goose version, got %d", appliedVersions)
	}

	var legacyVersion string
	if err := pool.QueryRow(ctx, `SELECT version FROM schema_migrations`).Scan(&legacyVersion); err != nil {
		t.Fatal(err)
	}
	if legacyVersion != "001_expenses.sql" {
		t.Fatal("legacy migration history changed")
	}

	created, err := repository.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID <= before.ID {
		t.Fatal("expense identity sequence was reset")
	}
}

func TestMigrationConcurrentIntegration(t *testing.T) {
	pool := unmigratedTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const concurrentRuns = 2
	results := make(chan error, concurrentRuns)

	for attempt := 0; attempt < concurrentRuns; attempt++ {
		go func() {
			results <- migrations.Apply(ctx, pool)
		}()
	}

	for attempt := 0; attempt < concurrentRuns; attempt++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Goose closed the application pool: %v", err)
	}

	var appliedVersions int
	const countAppliedVersions = `
		SELECT count(*)
		FROM goose_db_version
		WHERE version_id = 1 AND is_applied`

	err := pool.QueryRow(ctx, countAppliedVersions).Scan(&appliedVersions)
	if err != nil {
		t.Fatal(err)
	}
	if appliedVersions != 1 {
		t.Fatalf("migration applied %d times", appliedVersions)
	}
}

func TestMigrationDownUpIntegration(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		os.DirFS("../../migrations"),
		goose.WithDisableGlobalRegistry(true),
		goose.WithLogger(goose.NopLogger()),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Down only touches this test's random schema, never the application's tables.
	if _, err := provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	var expensesExist bool
	const checkExpenses = `SELECT to_regclass('expenses') IS NOT NULL`
	if err := pool.QueryRow(ctx, checkExpenses).Scan(&expensesExist); err != nil {
		t.Fatal(err)
	}
	if expensesExist {
		t.Fatal("Down did not remove expenses")
	}

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, checkExpenses).Scan(&expensesExist); err != nil {
		t.Fatal(err)
	}
	if !expensesExist {
		t.Fatal("Up did not recreate expenses after Down")
	}
}
