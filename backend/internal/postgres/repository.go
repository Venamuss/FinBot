package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type database interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Repository struct {
	pool *pgxpool.Pool
	db   database
	tx   pgx.Tx
}

const rollbackTimeout = time.Second

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, db: pool}
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

	row := r.db.QueryRow(ctx, query,
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

	row := r.db.QueryRow(ctx, query, id)
	return scanExpense(row)
}

func (r *Repository) Update(ctx context.Context, id int64, changes expense.Changes) (expense.Expense, error) {
	// Apply changes to the current row in one statement, preserving omitted fields
	// even when another request updates the same expense concurrently.
	// Advance the version by at least one database microsecond for stale selections.
	const query = `
		UPDATE expenses
		SET user_id = COALESCE($2::bigint, user_id),
		    amount = COALESCE($3::numeric, amount),
		    category = COALESCE($4::text, category),
		    spent_at = COALESCE($5::date, spent_at),
		    comment = CASE WHEN $6::boolean THEN $7::text ELSE comment END,
		    updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
		WHERE id = $1
		RETURNING ` + expenseColumns

	var amount *string
	if changes.Amount != nil {
		value := changes.Amount.String()
		amount = &value
	}
	row := r.db.QueryRow(ctx, query,
		id,
		changes.UserID,
		amount,
		changes.Category,
		changes.SpentAt,
		changes.CommentSet,
		changes.Comment,
	)
	return scanExpense(row)
}

func (r *Repository) Delete(ctx context.Context, id int64) error {
	result, err := r.db.Exec(ctx, `DELETE FROM expenses WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return expense.ErrNotFound
	}
	return nil
}

func (r *Repository) ListMembers(ctx context.Context) ([]expense.Member, error) {
	// UNION keeps newly discovered bot owners visible without creating metadata rows.
	const query = `
		SELECT owners.user_id, names.display_name
		FROM (
			SELECT user_id FROM expenses
			UNION
			SELECT user_id FROM member_names
		) AS owners
		LEFT JOIN member_names AS names ON names.user_id = owners.user_id
		ORDER BY owners.user_id ASC`
	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := []expense.Member{}
	for rows.Next() {
		var member expense.Member
		if err := rows.Scan(&member.UserID, &member.DisplayName); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return members, nil
}

func (r *Repository) SetMemberName(ctx context.Context, member expense.Member) (expense.Member, error) {
	const query = `
		INSERT INTO member_names (user_id, display_name)
		VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET display_name = EXCLUDED.display_name
		RETURNING user_id, display_name`

	var result expense.Member
	err := r.db.QueryRow(ctx, query, member.UserID, member.DisplayName).Scan(&result.UserID, &result.DisplayName)

	if err != nil {
		return expense.Member{}, err
	}
	return result, nil
}

func (r *Repository) List(ctx context.Context, filter expense.Filter) (expense.Page, error) {
	return r.list(ctx, filter, "")
}

func (r *Repository) list(ctx context.Context, filter expense.Filter, queryText string) (expense.Page, error) {
	orderBy, err := expenseOrderBy(filter)

	if err != nil {
		return expense.Page{}, err
	}
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 {
		return expense.Page{}, errors.New("invalid pagination or order")
	}

	whereClause, args := expenseWhereClause(filter)
	whereClause, args = commentWhereClause(whereClause, args, queryText)

	// A single repeatable-read snapshot keeps total and items consistent within a response.
	options := pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	}
	var tx pgx.Tx
	if r.tx != nil {
		// Bot operations already run in a REPEATABLE READ transaction. A savepoint
		// preserves the existing List cleanup without committing the outer unit.
		tx, err = r.tx.Begin(ctx)
	} else {
		tx, err = r.pool.BeginTx(ctx, options)
	}

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

func commentWhereClause(where string, args []any, query string) (string, []any) {
	if query != "" {
		args = append(args, query)
		where += fmt.Sprintf(" AND strpos(lower(COALESCE(comment, '')), lower($%d::text)) > 0", len(args))
	}
	return where, args
}
