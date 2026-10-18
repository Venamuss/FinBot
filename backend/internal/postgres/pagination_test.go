package postgres

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/conversation"
)

func TestTelegramPaginationNavigationIntegration(t *testing.T) {
	pool := oneConnectionPool(t, testPool(t))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Same-date records must retain id DESC across both page directions.
	const seed = `INSERT INTO expenses (user_id, amount, category, spent_at, comment)
		SELECT 101, 1, 'еда', DATE '2026-10-17', 'кофе' FROM generate_series(1, 29);
		INSERT INTO expenses (user_id, amount, category, spent_at)
		SELECT 202, 1, 'еда', DATE '2026-10-17' FROM generate_series(1, 21);
		INSERT INTO expenses (user_id, amount, category, spent_at, comment)
		VALUES (101, 1, 'еда', DATE '2024-02-29', 'кофе'),
		       (101, 1, 'транспорт', DATE '2026-10-17', 'кофе'),
		       (101, 1, 'еда', DATE '2026-10-17', 'чай')`
	if _, err := pool.Exec(ctx, seed); err != nil {
		t.Fatal(err)
	}
	model := &integrationModel{pool: pool}
	orchestrator := conversation.New(model, NewTelegramStore(pool), map[int64]bool{101: true, 202: true}, time.UTC)
	reference := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	firstA := []int64{29, 28, 27, 26, 25, 24, 23, 22, 21, 20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10}
	secondA := []int64{9, 8, 7, 6, 5, 4, 3, 2, 1}
	firstB := []int64{50, 49, 48, 47, 46, 45, 44, 43, 42, 41, 40, 39, 38, 37, 36, 35, 34, 33, 32, 31}
	unfiltered := []int64{53, 52, 29, 28, 27, 26, 25, 24, 23, 22, 21, 20, 19, 18, 17, 16, 15, 14, 13, 12}
	steps := []struct {
		owner int64
		text  string
		name  string
		args  string
		want  []int64
	}{
		{101, "траты на кофе в этом месяце", "list_expenses", `{"period":"this_month","category":"еда","query":"кофе"}`, firstA},
		{101, "ещё", "navigate_expenses", `{"direction":"next"}`, secondA},
		{202, "траты", "list_expenses", `{}`, firstB},
		{101, "назад", "navigate_expenses", `{"direction":"previous"}`, firstA},
		{101, "назад", "navigate_expenses", `{"direction":"previous"}`, firstA},
		{202, "ещё", "navigate_expenses", `{"direction":"next"}`, []int64{30}},
		{101, "ещё", "navigate_expenses", `{"direction":"next"}`, secondA},
		{101, "ещё", "navigate_expenses", `{"direction":"next"}`, nil},
		{101, "назад", "navigate_expenses", `{"direction":"previous"}`, firstA},
		{101, "траты", "list_expenses", `{}`, unfiltered},
		{101, "назад", "navigate_expenses", `{"direction":"previous"}`, unfiltered},
	}
	for index, step := range steps {
		model.name, model.args = step.name, step.args
		reply, err := orchestrator.Handle(ctx, int64(index+1), step.owner, step.text, reference)
		if err != nil {
			t.Fatal(err)
		}
		if step.want == nil {
			if reply != "Больше расходов нет." {
				t.Fatal("last-page boundary:", reply)
			}
			continue
		}
		var got []int64
		for _, line := range strings.Split(reply, "\n") {
			_, suffix, found := strings.Cut(line, " · #")
			if !found {
				continue
			}
			var id int64
			if _, err := fmt.Sscanf(suffix, "%d", &id); err != nil {
				t.Fatal(err)
			}
			got = append(got, id)
		}
		if !slices.Equal(got, step.want) {
			t.Fatalf("step %d: got %v, want %v", index+1, got, step.want)
		}
	}
	calls := model.calls
	reply, err := orchestrator.Handle(ctx, 1, 303, "назад", reference)
	if err != nil || reply != conversation.Denied || model.calls != calls {
		t.Fatal("denied user reached pagination or read a stored reply")
	}
	assertCounts(t, pool, 53, len(steps))
}
