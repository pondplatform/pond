package store

import (
	"context"
	"database/sql"
	"fmt"
)

// DBTX is the common interface satisfied by both *sql.DB and *sql.Tx.
// All store constructors accept DBTX so they can be used inside transactions.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Transactor begins a database transaction and runs fn inside it.
// If fn returns an error the transaction is rolled back; otherwise it is committed.
type Transactor interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context, db DBTX) error) error
}

type transactor struct{ db *sql.DB }

// NewTransactor returns a Transactor backed by db.
func NewTransactor(db *sql.DB) Transactor { return &transactor{db: db} }

func (t *transactor) RunInTx(ctx context.Context, fn func(ctx context.Context, db DBTX) error) error {
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
