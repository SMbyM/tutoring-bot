// Package store — хранилище на PostgreSQL (database/sql + lib/pq).
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strings"

	_ "github.com/lib/pq"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store работает либо поверх пула соединений, либо внутри транзакции (см. Tx).
type Store struct {
	db *sql.DB
	q  querier
}

func Open(ctx context.Context, url string) (*Store, error) {
	db, err := sql.Open("postgres", url)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("подключение к БД: %w", err)
	}
	return &Store{db: db, q: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Tx выполняет fn в транзакции. Вложенный вызов внутри транзакции переиспользует её.
func (s *Store) Tx(ctx context.Context, fn func(*Store) error) error {
	if _, inTx := s.q.(*sql.Tx); inTx {
		return fn(s)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&Store{db: s.db, q: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Migrate применяет встроенные SQL-миграции по порядку имён файлов.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var exists bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name=$1)`, name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		err = s.Tx(ctx, func(t *Store) error {
			if _, err := t.q.ExecContext(ctx, string(body)); err != nil {
				return fmt.Errorf("миграция %s: %w", name, err)
			}
			_, err := t.q.ExecContext(ctx, `INSERT INTO schema_migrations(name) VALUES ($1)`, name)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// ResetForTests удаляет все данные (только для тестов).
func (s *Store) ResetForTests(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`)
	if err != nil {
		return err
	}
	return s.Migrate(ctx)
}

func notFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
