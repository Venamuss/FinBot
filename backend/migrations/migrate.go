package migrations

import (
	"context"
	"embed"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed *.sql
var files embed.FS

// Apply serializes concurrent startups and applies each migration in one transaction.
func Apply(ctx context.Context, pool *pgxpool.Pool) (applyErr error) {
	// Closing the database/sql adapter releases its connections, not the pgx pool.
	db := stdlib.OpenDBFromPool(pool)
	defer func() {
		if err := db.Close(); err != nil {
			applyErr = errors.Join(applyErr, fmt.Errorf("close migration database: %w", err))
		}
	}()

	// Reuse the previous runner's lock key to coordinate with an older process.
	locker, err := lock.NewPostgresSessionLocker(
		lock.WithLockID(719230841),
		lock.WithLockTimeout(1, 30),
		lock.WithUnlockTimeout(1, 5),
	)
	if err != nil {
		return err
	}

	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		files,
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
		goose.WithLogger(goose.NopLogger()),
	)
	if err != nil {
		return err
	}
	defer provider.Close()

	_, err = provider.Up(ctx)
	return err
}
