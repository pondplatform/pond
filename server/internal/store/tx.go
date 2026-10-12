package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Repos bundles the repositories the deployment service writes through, all
// bound to the same DBTX.
type Repos struct {
	Deployments DeploymentInfoStore
	Services    ServiceRepository
	Envs        EnvironmentRepository
}

func NewRepos(db DBTX) Repos {
	return Repos{
		Deployments: NewDeploymentInfoStore(db),
		Services:    NewServiceStore(db),
		Envs:        NewEnvironmentStore(db),
	}
}

// Transactor runs fn in a database transaction, rolling back if fn returns an
// error.
type Transactor interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx Repos) error) error
}

type sqlTransactor struct{ db *sql.DB }

func NewTransactor(db *sql.DB) Transactor {
	return &sqlTransactor{db: db}
}

func (t *sqlTransactor) RunInTx(ctx context.Context, fn func(ctx context.Context, tx Repos) error) error {
	sqlTx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(ctx, NewRepos(sqlTx)); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
