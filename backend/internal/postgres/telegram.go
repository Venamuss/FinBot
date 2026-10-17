package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/Venamuss/FinBot/internal/conversation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const updateTimeout = 5 * time.Second

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

// Execute accepts only local database work. No HTTP operation may run inside
// the callback. A unique update_id serializes concurrent delivery across processes.
func (store *TelegramStore) Execute(ctx context.Context, updateID, owner int64, execute func(context.Context, conversation.Repositories) (string, error)) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()
	for attempt := range 3 {
		response, committed, err := store.executeOnce(ctx, updateID, owner, execute)
		if !retryTransaction(err) || attempt == 2 || ctx.Err() != nil {
			return response, committed, err
		}
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
		return "", false, err
	}
	defer rollbackTransaction(tx)
	result, err := tx.Exec(ctx, `
  INSERT INTO telegram_updates (update_id, user_id, response)
  VALUES ($1, $2, '')
  ON CONFLICT (update_id) DO NOTHING`, updateID, owner)
	if err != nil {
		return "", false, err
	}
	if result.RowsAffected() == 0 {
		response, found, err := lookupResponse(ctx, tx, updateID, owner)
		if err != nil {
			return "", false, err
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
		return "", false, err
	}
	if response == "" {
		return "", false, errors.New("empty processing result")
	}
	_, err = tx.Exec(ctx, `UPDATE telegram_updates SET response = $3 WHERE update_id = $1 AND user_id = $2`, updateID, owner, response)
	if err != nil {
		return "", false, err
	}
	if err := tx.Commit(ctx); err != nil {
		// An uncertain commit is retried by update ID, never assumed to have failed.
		return "", false, err
	}
	return response, true, nil
}

func rollbackTransaction(tx pgx.Tx) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
	defer cancel()
	// Preserve the original error; pgx discards unusable connections. Also, safe
	// after a successful Commit, when Rollback returns ErrTxClosed.
	_ = tx.Rollback(cleanupCtx)
}
