package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/Venamuss/FinBot/internal/conversation"
	"github.com/Venamuss/FinBot/internal/observability"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const updateTimeout = 5 * time.Second

// transactionError exposes only a fixed stage in its message. The cause remains
// available to errors.Is/As, but must never be formatted into logs.
type transactionError struct {
	stage string
	cause error
}

func (err *transactionError) Error() string {
	return "Telegram transaction failed at " + err.stage
}

func (err *transactionError) Unwrap() error {
	return err.cause
}

type TelegramStore struct {
	pool *pgxpool.Pool
}

func NewTelegramStore(pool *pgxpool.Pool) *TelegramStore {
	return &TelegramStore{pool: pool}
}

func (store *TelegramStore) Lookup(ctx context.Context, updateID, owner int64) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()

	return lookupResponse(ctx, store.pool, updateID, owner)
}

func lookupResponse(ctx context.Context, db database, updateID, owner int64) (string, bool, error) {
	var response string

	err := db.QueryRow(ctx, `SELECT response FROM telegram_updates WHERE update_id = $1 AND user_id = $2`, updateID, owner).Scan(&response)

	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if response == "" {
		return "", false, errors.New("empty processing result")
	}
	return response, true, nil
}

// Execute accepts only database work using the supplied repositories and context.
// The callback may run again after rollback: it must rebuild any derived in-memory
// state on each attempt and must not perform external or nontransactional effects.
// A unique update_id serializes concurrent delivery across processes.
//
// committed is true only for a newly committed result. With a nil error, false
// means replay or rejection of an update owned by someone else. With an error,
// false does not rule out an uncertain commit; retry using the same update_id.
func (store *TelegramStore) Execute(ctx context.Context, updateID, owner int64, execute func(context.Context, conversation.Repositories) (string, error)) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()

	logger := observability.Logger(observability.WithUpdateID(ctx, updateID), "postgres", "process_update")
	started := time.Now()

	for attempt := range 3 {
		response, committed, err := store.executeOnce(ctx, updateID, owner, execute)
		attemptLogger := logger.With("attempt", attempt+1)
		var failure *transactionError
		if errors.As(err, &failure) {
			attemptLogger = attemptLogger.With("stage", failure.stage)
		}
		if !retryTransaction(err) || attempt == 2 || ctx.Err() != nil {
			if err != nil {
				attemptLogger.DebugContext(ctx, "Telegram transaction failed", "error_category", observability.Category(err), "duration_ms", time.Since(started).Milliseconds())
			}
			return response, committed, err
		}
		attemptLogger.WarnContext(ctx, "Telegram transaction retry scheduled", "error_category", observability.Category(err))
	}
	return "", false, errors.New("processing transaction failed")
}

func retryTransaction(err error) bool {
	var databaseError *pgconn.PgError
	return errors.As(err, &databaseError) && (databaseError.Code == "40001" || databaseError.Code == "40P01")
}

func (store *TelegramStore) executeOnce(ctx context.Context, updateID, owner int64, execute func(context.Context, conversation.Repositories) (string, error)) (string, bool, error) {
	// Match List's snapshot guarantees. PostgreSQL can reject a concurrent insert
	// under this isolation level; Execute retries only the local transaction.
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return "", false, &transactionError{stage: "begin", cause: err}
	}
	defer rollbackTransaction(tx)

	result, err := tx.Exec(ctx, `
  INSERT INTO telegram_updates (update_id, user_id, response)
  VALUES ($1, $2, '')
  ON CONFLICT (update_id) DO NOTHING`, updateID, owner)
	if err != nil {
		return "", false, &transactionError{stage: "reserve_update", cause: err}
	}

	if result.RowsAffected() == 0 {
		response, found, err := lookupResponse(ctx, tx, updateID, owner)
		if err != nil {
			return "", false, &transactionError{stage: "lookup_response", cause: err}
		}
		if !found {
			// A different owner cannot retrieve a stored response or reuse its update ID.
			return "Запрос недоступен.", false, nil
		}
		return response, false, nil
	}

	repo := &Repository{pool: store.pool, db: tx, tx: tx}
	response, err := execute(ctx, repo)
	if err != nil {
		return "", false, &transactionError{stage: "execute", cause: err}
	}
	if response == "" {
		return "", false, &transactionError{stage: "execute", cause: errors.New("empty processing result")}
	}

	_, err = tx.Exec(ctx, `UPDATE telegram_updates SET response = $3 WHERE update_id = $1 AND user_id = $2`, updateID, owner, response)
	if err != nil {
		return "", false, &transactionError{stage: "save_response", cause: err}
	}

	if err := tx.Commit(ctx); err != nil {
		// An uncertain commit is retried by update ID, never assumed to have failed.
		return "", false, &transactionError{stage: "commit", cause: err}
	}

	return response, true, nil
}
