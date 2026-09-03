// Package storage owns the Postgres pool and the schema it runs against.
package storage

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: opening the pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("storage: reaching the database: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Pool() *pgxpool.Pool { return s.pool }
func (s *Store) Close()              { s.pool.Close() }

// Migrate applies every embedded migration that has not run yet, each in its
// own transaction so a failure leaves the schema on the last good step.
func (s *Store) Migrate(ctx context.Context) error {
	const ledger = `CREATE TABLE IF NOT EXISTS schema_migrations (
		name       TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`
	if _, err := s.pool.Exec(ctx, ledger); err != nil {
		return fmt.Errorf("storage: preparing the migration ledger: %w", err)
	}

	names, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)

	for _, name := range names {
		var applied bool
		err := s.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)`, name,
		).Scan(&applied)
		if err != nil {
			return fmt.Errorf("storage: checking migration %s: %w", name, err)
		}
		if applied {
			continue
		}

		body, err := migrationFiles.ReadFile(name)
		if err != nil {
			return err
		}
		if err := s.inTx(ctx, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(body)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, name)
			return err
		}); err != nil {
			return fmt.Errorf("storage: applying migration %s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
