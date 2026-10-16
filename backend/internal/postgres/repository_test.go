package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := unmigratedTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for attempt := 0; attempt < 2; attempt++ {
		if err := migrations.Apply(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	return pool
}

// Each run owns a random schema; existing tables and records are never truncated.
func unmigratedTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; real PostgreSQL integration tests skipped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	adminPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)
	if err := adminPool.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	schemaName := "finbot_test_" + hex.EncodeToString(suffix[:])
	quotedSchema := pgx.Identifier{schemaName}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := adminPool.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestRepositoryIntegration(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repository := New(pool)
	comment := "такси '); DROP TABLE expenses; -- <script>"
	inputs := []expense.Input{
		{
			UserID:   "101",
			Amount:   "0.10",
			Category: "еда",
			SpentAt:  "2026-10-15",
		},
		{
			UserID:   "202",
			Amount:   "10",
			Category: "транспорт",
			SpentAt:  "2026-10-16",
			Comment:  &comment,
		},
		{
			UserID:   "101",
			Amount:   "2",
			Category: "еда",
			SpentAt:  "2026-10-16",
		},
		{
			UserID:   "101",
			Amount:   "0.20",
			Category: "транспорт",
			SpentAt:  "2026-10-17",
		},
		{
			UserID:   "9223372036854775807",
			Amount:   "999999999999.99",
			Category: "другое",
			SpentAt:  "2024-02-29",
		},
	}
	created := []expense.Expense{}
	for _, input := range inputs {
		validated, err := input.Validate()
		if err != nil {
			t.Fatal(err)
		}
		item, err := repository.Create(ctx, validated)
		if err != nil {
			t.Fatal(err)
		}
		if item.ID <= 0 || item.CreatedAt.IsZero() || !item.CreatedAt.Equal(item.UpdatedAt) {
			t.Fatalf("server fields: %+v", item)
		}
		created = append(created, item)

		got, err := repository.GetByID(ctx, item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != item.ID || got.UserID != item.UserID {
			t.Fatalf("identity changed: want %d/%d, got %d/%d", item.ID, item.UserID, got.ID, got.UserID)
		}
		if got.Amount != item.Amount || got.SpentAt != item.SpentAt || got.Category != item.Category {
			t.Fatalf("expense fields changed: want %+v, got %+v", item, got)
		}
		if !got.CreatedAt.Equal(item.CreatedAt) {
			t.Fatalf("created_at changed: want %s, got %s", item.CreatedAt, got.CreatedAt)
		}
		if (got.Comment == nil) != (item.Comment == nil) {
			t.Fatal("comment nullability changed")
		}
		if got.Comment != nil && *got.Comment != *item.Comment {
			t.Fatal("comment changed")
		}
	}
	if _, err := repository.GetByID(ctx, 9223372036854775807); !errors.Is(err, expense.ErrNotFound) {
		t.Fatalf("not found: %v", err)
	}
	tests := []struct {
		name   string
		filter expense.Filter
		total  int64
		ids    []int64
	}{
		{
			name:   "default",
			filter: expense.Filter{},
			total:  5,
			ids:    []int64{created[3].ID, created[2].ID, created[1].ID, created[0].ID, created[4].ID},
		},
		{
			name: "owner A",
			filter: expense.Filter{
				UserID: 101,
			},
			total: 3,
			ids:   []int64{created[3].ID, created[2].ID, created[0].ID},
		},
		{
			name: "owner B",
			filter: expense.Filter{
				UserID: 202,
			},
			total: 1,
			ids:   []int64{created[1].ID},
		},
		{
			name: "category",
			filter: expense.Filter{
				Category: "еда",
			},
			total: 2,
			ids:   []int64{created[2].ID, created[0].ID},
		},
		{
			name: "inclusive period",
			filter: expense.Filter{
				DateFrom: "2026-10-15",
				DateTo:   "2026-10-16",
			},
			total: 3,
			ids:   []int64{created[2].ID, created[1].ID, created[0].ID},
		},
		{
			name: "combined AND",
			filter: expense.Filter{
				UserID:   101,
				Category: "еда",
				DateFrom: "2026-10-16",
				DateTo:   "2026-10-16",
			},
			total: 1,
			ids:   []int64{created[2].ID},
		},
		{
			name: "numeric amount",
			filter: expense.Filter{
				Sort:  "amount",
				Order: "asc",
			},
			total: 5,
			ids:   []int64{created[0].ID, created[3].ID, created[2].ID, created[1].ID, created[4].ID},
		},
		{
			name: "id asc",
			filter: expense.Filter{
				Sort:  "id",
				Order: "asc",
			},
			total: 5,
			ids:   []int64{created[0].ID, created[1].ID, created[2].ID, created[3].ID, created[4].ID},
		},
		{
			name: "page",
			filter: expense.Filter{
				UserID: 101,
				Limit:  1,
				Offset: 1,
			},
			total: 3,
			ids:   []int64{created[2].ID},
		},
		{
			name: "past end",
			filter: expense.Filter{
				UserID: 101,
				Offset: 100,
			},
			total: 3,
			ids:   []int64{},
		},
		{
			name: "unknown owner",
			filter: expense.Filter{
				UserID: 303,
			},
			total: 0,
			ids:   []int64{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			filter := tc.filter
			if filter.Sort == "" {
				filter.Sort = "spent_at"
			}
			if filter.Order == "" {
				filter.Order = "desc"
			}
			if filter.Limit == 0 {
				filter.Limit = 50
			}
			page, err := repository.List(ctx, filter)
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != tc.total {
				t.Fatalf("want total %d, got %d", tc.total, page.Total)
			}
			if page.Items == nil || len(page.Items) != len(tc.ids) {
				t.Fatalf("want %d items in a non-nil slice, got %+v", len(tc.ids), page.Items)
			}
			if page.Limit != filter.Limit || page.Offset != filter.Offset {
				t.Fatalf("pagination: want limit=%d offset=%d, got limit=%d offset=%d",
					filter.Limit, filter.Offset, page.Limit, page.Offset)
			}
			for i, id := range tc.ids {
				if page.Items[i].ID != id {
					t.Fatalf("position %d: want %d got %d", i, id, page.Items[i].ID)
				}
			}
		})
	}
	// Equal values use id in the same direction, including timestamps.
	const equalizeSortValues = `
		UPDATE expenses
		SET created_at = '2026-10-16T09:00:00Z', amount = 1`
	if _, err := pool.Exec(ctx, equalizeSortValues); err != nil {
		t.Fatal(err)
	}
	for _, sortField := range []string{"spent_at", "created_at", "amount", "id"} {
		for _, order := range []string{"asc", "desc"} {
			page, err := repository.List(ctx, expense.Filter{Sort: sortField, Order: order, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			// The default date tie direction is checked by the cases above.
			if sortField == "spent_at" {
				continue
			}
			for i := 1; i < len(page.Items); i++ {
				if (page.Items[i-1].ID < page.Items[i].ID) != (order == "asc") {
					t.Fatalf("unstable %s %s", sortField, order)
				}
			}
		}
	}
	if _, err := repository.List(ctx, expense.Filter{Sort: "id; DROP TABLE expenses", Order: "desc", Limit: 50}); err == nil {
		t.Fatal("unsafe sort accepted")
	}
}

func TestDatabaseConstraintsIntegration(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tests := []struct {
		name  string
		query string
		code  string
	}{
		{
			name: "owner positive",
			query: `
				INSERT INTO expenses (user_id,amount,category,spent_at)
				VALUES (0,1,'еда','2026-10-16')`,
			code: "23514",
		},
		{
			name: "owner required",
			query: `
				INSERT INTO expenses (amount,category,spent_at)
				VALUES (1,'еда','2026-10-16')`,
			code: "23502",
		},
		{
			name: "zero",
			query: `
				INSERT INTO expenses (user_id,amount,category,spent_at)
				VALUES (1,0,'еда','2026-10-16')`,
			code: "23514",
		},
		{
			name: "negative",
			query: `
				INSERT INTO expenses (user_id,amount,category,spent_at)
				VALUES (1,-1,'еда','2026-10-16')`,
			code: "23514",
		},
		{
			name: "overflow",
			query: `
				INSERT INTO expenses (user_id,amount,category,spent_at)
				VALUES (1,1000000000000,'еда','2026-10-16')`,
			code: "22003",
		},
		{
			name: "NaN",
			query: `
				INSERT INTO expenses (user_id,amount,category,spent_at)
				VALUES (1,'NaN','еда','2026-10-16')`,
			code: "23514",
		},
		{
			name: "category",
			query: `
				INSERT INTO expenses (user_id,amount,category,spent_at)
				VALUES (1,1,'food','2026-10-16')`,
			code: "23514",
		},
		{
			name: "date required",
			query: `
				INSERT INTO expenses (user_id,amount,category)
				VALUES (1,1,'еда')`,
			code: "23502",
		},
		{
			name: "infinite date",
			query: `
				INSERT INTO expenses (user_id,amount,category,spent_at)
				VALUES (1,1,'еда','infinity')`,
			code: "23514",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, tc.query)
			var dbErr *pgconn.PgError
			if !errors.As(err, &dbErr) || dbErr.Code != tc.code {
				t.Fatalf("want SQLSTATE %s got %v", tc.code, err)
			}
		})
	}
	var indexDefinition string
	const findOwnerDateIndex = `
		SELECT indexdef
		FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND indexname = 'expenses_user_date_idx'`
	if err := pool.QueryRow(ctx, findOwnerDateIndex).Scan(&indexDefinition); err != nil {
		t.Fatal(err)
	}
	if indexDefinition == "" {
		t.Fatal("owner/date index missing")
	}
	for _, date := range []string{"0001-01-01", "9999-12-31"} {
		input := expense.Input{
			UserID:   "1",
			Amount:   "0.01",
			Category: "другое",
			SpentAt:  date,
		}
		validated, err := input.Validate()
		if err != nil {
			t.Fatal(err)
		}
		got, err := New(pool).Create(ctx, validated)
		if err != nil {
			t.Fatalf("date boundary %s: %v", date, err)
		}
		if got.SpentAt != expense.Date(date) {
			t.Fatalf("date boundary: want %s, got %s", date, got.SpentAt)
		}
	}
}
