package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lib/pq"
)

// DBTX is the common interface satisfied by both *sql.DB and *sql.Tx.
// All store constructors accept DBTX so they can be used inside transactions.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
)

// pgCode returns the Postgres SQLSTATE of err, or "" if err is not a
// Postgres error. Stores use it to report constraint violations caused by the
// request as user errors instead of internal ones.
func pgCode(err error) string {
	var pe *pq.Error
	if errors.As(err, &pe) {
		return string(pe.Code)
	}
	return ""
}
