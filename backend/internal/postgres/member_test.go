package postgres

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/migrations"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestMemberNamesIntegration(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repository := New(pool)
	service := expense.NewService(repository)
	first := createTestExpense(t, ctx, service, "101")
	second := createTestExpense(t, ctx, service, "202")
	assertMembers(t, ctx, repository, []int64{101, 202})
	name := "  Анна  "
	for _, id := range []string{"101", "202", "9223372036854775807"} {
		member, err := service.SetMemberName(ctx, id, &name)
		if err != nil {
			t.Fatal(err)
		}
		if member.DisplayName == nil || *member.DisplayName != "Анна" {
			t.Fatal("trimmed name was not persisted")
		}
	}
	// Names may repeat and IDs with metadata are visible before their first expense.
	assertMembers(t, ctx, repository, []int64{101, 202, 9223372036854775807})
	name = "Мария '); DROP TABLE expenses; -- <script>"
	if _, err := service.SetMemberName(ctx, "101", &name); err != nil {
		t.Fatal(err)
	}
	for _, original := range []expense.Expense{first, second} {
		stored, err := repository.GetByID(ctx, original.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(stored, original) {
			t.Fatal("renaming changed an expense, its owner or its version")
		}
	}
	if _, err := repository.GetOwned(ctx, second.UserID, first.ID); !errors.Is(err, expense.ErrNotFound) {
		t.Fatal("display names bypassed SQL ownership")
	}
	members, err := repository.ListMembers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if members[0].DisplayName == nil || *members[0].DisplayName != name {
		t.Fatal("name was not stored as literal data")
	}
	if _, err := service.SetMemberName(ctx, "101", nil); err != nil {
		t.Fatal(err)
	}
	if err := repository.Delete(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	// Explicit metadata (even a cleared name) persists after the last expense.
	assertMembers(t, ctx, repository, []int64{101, 202, 9223372036854775807})
	third := createTestExpense(t, ctx, service, "303")
	owned, err := repository.GetOwned(ctx, 303, third.ID)
	if err != nil || owned.UserID != 303 {
		t.Fatal("newly discovered owner was not preserved")
	}
	members, err = repository.ListMembers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 4 || members[0].DisplayName != nil || members[2].UserID != 303 || members[2].DisplayName != nil {
		t.Fatal("cleared or newly discovered member missing numeric fallback")
	}
	if err := repository.Delete(ctx, third.ID); err != nil {
		t.Fatal(err)
	}
	assertMembers(t, ctx, repository, []int64{101, 202, 9223372036854775807})
}

func TestMemberNamesMigrationIntegration(t *testing.T) {
	pool := unmigratedTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../migrations"),
		goose.WithDisableGlobalRegistry(true), goose.WithLogger(goose.NopLogger()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 2); err != nil {
		t.Fatal(err)
	}
	repository := New(pool)
	before := createTestExpense(t, ctx, expense.NewService(repository), "101")
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	after, err := repository.GetByID(ctx, before.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("migration changed existing expenses")
	}
	assertMembers(t, ctx, repository, []int64{101})
	if _, err := provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	after, err = repository.GetByID(ctx, before.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("member metadata rollback changed existing expenses")
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	assertMembers(t, ctx, repository, []int64{101})
}
