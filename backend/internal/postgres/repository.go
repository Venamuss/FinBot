package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

const rollbackTimeout = time.Second

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const expenseColumns = `
	id,
	user_id,
	amount::text AS amount_text,
	category,
	to_char(spent_at, 'YYYY-MM-DD'),
	comment,
	created_at,
	updated_at`

func scanExpense(row pgx.Row) (expense.Expense, error) {
	var result expense.Expense
	var amountText, spentAtText string
	err := row.Scan(
		&result.ID,
		&result.UserID,
		&amountText,
		&result.Category,
		&spentAtText,
		&result.Comment,
		&result.CreatedAt,
		&result.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return result, expense.ErrNotFound
		}
		return result, err
	}

	result.Amount, err = expense.ParseAmount(amountText)
	if err != nil {
		return expense.Expense{}, err
	}
	result.SpentAt, err = expense.ParseDate(spentAtText)
	if err != nil {
		return expense.Expense{}, err
	}

	result.CreatedAt = result.CreatedAt.UTC()
	result.UpdatedAt = result.UpdatedAt.UTC()
	return result, nil
}

func (r *Repository) Create(ctx context.Context, input expense.Expense) (expense.Expense, error) {
	const query = `
		INSERT INTO expenses (user_id, amount, category, spent_at, comment)
		VALUES ($1, $2::numeric, $3, $4::date, $5)
		RETURNING ` + expenseColumns

	row := r.pool.QueryRow(ctx, query,
		input.UserID,
		input.Amount.String(),
		string(input.Category),
		string(input.SpentAt),
		input.Comment,
	)
	return scanExpense(row)
}

func (r *Repository) GetByID(ctx context.Context, id int64) (expense.Expense, error) {
	const query = `
		SELECT ` + expenseColumns + `
		FROM expenses
		WHERE id = $1`

	row := r.pool.QueryRow(ctx, query, id)
	return scanExpense(row)
}

func (r *Repository) List(ctx context.Context, filter expense.Filter) (expense.Page, error) {
	orderBy, err := expenseOrderBy(filter)
	if err != nil {
		return expense.Page{}, err
	}
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 {
		return expense.Page{}, errors.New("invalid pagination or order")
	}
	whereClause, args := expenseWhereClause(filter)

	// A single repeatable-read snapshot keeps total and items consistent within a response.
	options := pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	}
	tx, err := r.pool.BeginTx(ctx, options)
	if err != nil {
		return expense.Page{}, err
	}
	defer func() {
		// Cleanup must outlive request cancellation but stay bounded if the connection
		// is unavailable. Preserve the operation's error; pgx discards a failed connection.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
		defer cancel()
		_ = tx.Rollback(cleanupCtx)
	}()

	page := expense.Page{
		Items:  []expense.Expense{},
		Limit:  filter.Limit,
		Offset: filter.Offset,
	}
	countQuery := "SELECT count(*) FROM expenses WHERE " + whereClause
	err = tx.QueryRow(ctx, countQuery, args...).Scan(&page.Total)
	if err != nil {
		return expense.Page{}, err
	}

	query := fmt.Sprintf(`
		SELECT %s
		FROM expenses
		WHERE %s
		ORDER BY %s
		LIMIT $%d OFFSET $%d`,
		expenseColumns,
		whereClause,
		orderBy,
		len(args)+1,
		len(args)+2,
	)
	args = append(args, filter.Limit, filter.Offset)
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return expense.Page{}, err
	}
	defer rows.Close()

	for rows.Next() {
		item, err := scanExpense(rows)
		if err != nil {
			return expense.Page{}, err
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return expense.Page{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return expense.Page{}, err
	}
	return page, nil
}

func expenseOrderBy(filter expense.Filter) (string, error) {
	// SQL identifiers come only from these fixed maps, even for internal callers.
	sortColumns := map[string]string{
		"spent_at":   "spent_at",
		"created_at": "created_at",
		"amount":     "amount",
		"id":         "id",
	}
	sortColumn, ok := sortColumns[filter.Sort]
	if !ok {
		return "", errors.New("invalid sort")
	}
	directions := map[string]string{
		"asc":  "ASC",
		"desc": "DESC",
	}
	direction, ok := directions[filter.Order]
	if !ok {
		return "", errors.New("invalid pagination or order")
	}

	orderBy := sortColumn + " " + direction
	if sortColumn != "id" {
		orderBy += ", id " + direction
	}
	return orderBy, nil
}

func expenseWhereClause(filter expense.Filter) (string, []any) {
	clauses := []string{"TRUE"}
	args := []any{}
	if filter.UserID != 0 {
		args = append(args, filter.UserID)
		clauses = append(clauses, fmt.Sprintf("user_id = $%d", len(args)))
	}
	if filter.DateFrom != "" {
		args = append(args, string(filter.DateFrom))
		clauses = append(clauses, fmt.Sprintf("spent_at >= $%d::date", len(args)))
	}
	if filter.DateTo != "" {
		args = append(args, string(filter.DateTo))
		clauses = append(clauses, fmt.Sprintf("spent_at <= $%d::date", len(args)))
	}
	if filter.Category != "" {
		args = append(args, string(filter.Category))
		clauses = append(clauses, fmt.Sprintf("category = $%d", len(args)))
	}
	return strings.Join(clauses, " AND "), args
}
