package postgres

import (
	"context"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
)

var _ expense.OwnerRepository = (*Repository)(nil)

func (r *Repository) GetOwned(ctx context.Context, owner, id int64) (expense.Expense, error) {
	const query = `SELECT ` + expenseColumns + ` FROM expenses WHERE user_id = $1 AND id = $2`
	return scanExpense(r.pool.QueryRow(ctx, query, owner, id))
}

func (r *Repository) LatestOwned(ctx context.Context, owner int64) (expense.Expense, error) {
	const query = `
		SELECT ` + expenseColumns + ` FROM expenses
		WHERE user_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1`
	return scanExpense(r.pool.QueryRow(ctx, query, owner))
}

func (r *Repository) ListOwned(ctx context.Context, owner int64, filter expense.OwnerFilter, limit, offset int) (expense.Page, error) {
	if owner <= 0 {
		return expense.Page{}, expense.ErrInvalidID
	}
	return r.List(ctx, expense.Filter{
		UserID:   owner,
		DateFrom: filter.DateFrom,
		DateTo:   filter.DateTo,
		Category: filter.Category,
		Sort:     "spent_at",
		Order:    "desc",
		Limit:    limit,
		Offset:   offset,
	})
}

func (r *Repository) TotalOwned(ctx context.Context, owner int64, filter expense.OwnerFilter) (string, error) {
	// Reject the unscoped sentinel before reusing administrative filter building.
	if owner <= 0 {
		return "", expense.ErrInvalidID
	}
	where, args := expenseWhereClause(expense.Filter{
		UserID:   owner,
		DateFrom: filter.DateFrom,
		DateTo:   filter.DateTo,
		Category: filter.Category,
	})
	query := `SELECT COALESCE(SUM(amount), 0.00)::text FROM expenses WHERE ` + where
	var total string
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return "", err
	}
	return total, nil
}

func (r *Repository) UpdateOwned(ctx context.Context, owner, id int64, expectedUpdatedAt time.Time, changes expense.Changes) (expense.Expense, error) {
	// No owner assignment is allowed here, even if Changes came from an internal
	// caller. PostgreSQL rechecks both guards after waiting for a concurrent writer.
	const query = `
		UPDATE expenses
		SET amount = COALESCE($4::numeric, amount),
		    category = COALESCE($5::text, category),
		    spent_at = COALESCE($6::date, spent_at),
		    comment = CASE WHEN $7::boolean THEN $8::text ELSE comment END,
		    updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
		WHERE user_id = $1 AND id = $2 AND updated_at = $3
		RETURNING ` + expenseColumns
	var amount *string
	if changes.Amount != nil {
		value := changes.Amount.String()
		amount = &value
	}
	row := r.pool.QueryRow(ctx, query,
		owner,
		id,
		expectedUpdatedAt,
		amount,
		changes.Category,
		changes.SpentAt,
		changes.CommentSet,
		changes.Comment,
	)
	return scanExpense(row)
}

func (r *Repository) DeleteOwned(ctx context.Context, owner, id int64, expectedUpdatedAt time.Time) error {
	const query = `DELETE FROM expenses WHERE user_id = $1 AND id = $2 AND updated_at = $3`
	result, err := r.pool.Exec(ctx, query, owner, id, expectedUpdatedAt)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return expense.ErrNotFound
	}
	return nil
}
