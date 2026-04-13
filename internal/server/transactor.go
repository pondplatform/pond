package server

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pondplatform/pond/internal/server/service"
	"github.com/pondplatform/pond/internal/server/store"
)

// serverTransactor implements service.Transactor by opening a *sql.Tx and
// constructing tx-scoped store instances for each transaction.
type serverTransactor struct{ db *sql.DB }

func newTransactor(db *sql.DB) service.Transactor {
	return &serverTransactor{db: db}
}

func (t *serverTransactor) RunInTx(ctx context.Context, fn func(ctx context.Context, tx service.TxRepos) error) error {
	sqlTx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	repos := service.TxRepos{
		Deployments: store.NewDeploymentStore(sqlTx),
		DepRequests: store.NewDependencyRequestStore(sqlTx),
		Commands:    store.NewCommandStore(sqlTx),
	}
	if err := fn(ctx, repos); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	return sqlTx.Commit()
}
