package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"time"

	_ "github.com/lib/pq"
	"github.com/pondplatform/pond/internal/server/api"
	"github.com/pondplatform/pond/internal/server/dependency"
	"github.com/pondplatform/pond/internal/server/helmgen"
	"github.com/pondplatform/pond/internal/server/queue"
	"github.com/pondplatform/pond/internal/server/service"
	"github.com/pondplatform/pond/internal/server/store"
)

type Config struct {
	DatabaseURL string
	ListenAddr  string
}

func Run(ctx context.Context, cfg Config) error {
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}

	// Repositories
	envStore := store.NewEnvironmentStore(db)
	serviceStore := store.NewServiceStore(db)
	deploymentStore := store.NewDeploymentStore(db)
	depConfigStore := store.NewDependencyConfigStore(db)
	depRequestStore := store.NewDependencyRequestStore(db)
	resolvedCtxStore := store.NewResolvedContextStore(db)
	clusterStore := store.NewClusterStore(db)
	cmdQueue := queue.NewCommandQueue(db)

	// Registries
	specRegistry := dependency.NewSpecRegistry()
	providerRegistry := dependency.NewProviderRegistry()

	// Services
	depResolver := dependency.NewDependencyResolver(depConfigStore, resolvedCtxStore, specRegistry, providerRegistry)
	helmGenerator := helmgen.NewGenerator()
	deploySvc := service.NewDeploymentService(deploymentStore, serviceStore, envStore, depConfigStore, depRequestStore, depResolver, helmGenerator, cmdQueue)
	advancer := service.NewDeploymentAdvancer(deploymentStore, envStore, depConfigStore, depRequestStore, helmGenerator, cmdQueue)

	// Agent handler
	agentHandler := api.NewAgentHandler(clusterStore, cmdQueue, advancer)

	// HTTP router
	router := api.NewRouter(deploySvc, serviceStore, envStore, depConfigStore, resolvedCtxStore, agentHandler)

	// Requeue commands that were claimed by an agent that crashed before
	// acknowledging. Runs every 30 seconds; resets claims older than 5 minutes.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := cmdQueue.RequeueStaleClaims(ctx, 5*time.Minute); err != nil {
					log.Printf("requeue stale claims: %v", err)
				}
			}
		}
	}()

	log.Printf("server listening on %s", cfg.ListenAddr)
	server := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: router,
	}

	go func() {
		<-ctx.Done()
		server.Shutdown(context.Background())
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("listen: %w", err)
	}
	return nil
}
