package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"

	_ "github.com/lib/pq"
	"github.com/pondplatform/pond/internal/common/config"
	"github.com/pondplatform/pond/internal/server/api"
	"github.com/pondplatform/pond/internal/server/dependency"
	"github.com/pondplatform/pond/internal/server/events"
	"github.com/pondplatform/pond/internal/server/helmgen"
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
	deploymentInfoStore := store.NewDeploymentInfoStore(db)
	orgStore := store.NewOrganizationStore(db)
	projectStore := store.NewProjectStore(db)
	envStore := store.NewEnvironmentStore(db)
	serviceStore := store.NewServiceStore(db)
	clusterStore := store.NewClusterStore(db)

	// Transactor for multi-step atomic writes in services
	tx := newTransactor(db)

	// Spec registry for dependency type validation
	specRegistry := dependency.NewSpecRegistry()

	// Event bus
	bus := events.NewMemoryBus()

	// Services
	depSvc := service.NewDependencyService(specRegistry)
	helmGenerator := helmgen.NewGenerator()
	tmplRenderer := config.NewTemplateRenderer()
	resolver := config.NewResolver()
	deploySvc := service.NewDeploymentService(deploymentInfoStore, serviceStore, envStore, depSvc, helmGenerator, tmplRenderer, resolver, tx, bus)

	// Start the deployment service event loop (subscribes to command.results topic).
	go deploySvc.Start(ctx)

	// Agent handler
	agentHandler := api.NewAgentHandler(clusterStore, bus)

	// HTTP router
	router := api.NewRouter(api.RouterDeps{
		DeploySvc:    deploySvc,
		Orgs:         orgStore,
		Projects:     projectStore,
		Envs:         envStore,
		Services:     serviceStore,
		Clusters:     clusterStore,
		SpecRegistry: specRegistry,
		AgentHandler: agentHandler,
	})

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
