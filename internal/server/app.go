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
	"github.com/pondplatform/pond/internal/server/auth"
	"github.com/pondplatform/pond/internal/server/dependency"
	"github.com/pondplatform/pond/internal/server/events"
	"github.com/pondplatform/pond/internal/server/helmgen"
	"github.com/pondplatform/pond/internal/server/service"
	"github.com/pondplatform/pond/internal/server/store"
)

type Config struct {
	DatabaseURL string
	ListenAddr  string
	RabbitMQURL string
	// AdminKey grants unrestricted access to all API endpoints when set.
	// Configured via the POND_ADMIN_KEY environment variable.
	AdminKey  string
	JWTSecret string
}

func Run(ctx context.Context, cfg Config) error {
	if cfg.JWTSecret == "" {
		return fmt.Errorf("POND_JWT_SECRET must be set")
	}

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
	bus, closeBus, err := events.NewRabbitMQBus(cfg.RabbitMQURL)
	if err != nil {
		return fmt.Errorf("rabbitmq: %w", err)
	}
	defer closeBus()

	// Services
	depSvc := service.NewDependencyService(specRegistry)
	helmGenerator := helmgen.NewGenerator()
	tmplRenderer := config.NewTemplateRenderer()
	resolver := config.NewResolver()
	deploySvc := service.NewDeploymentService(deploymentInfoStore, serviceStore, envStore, depSvc, helmGenerator, tmplRenderer, resolver, tx, bus)

	// Start the deployment service event loop (subscribes to all state-machine topics).
	svcErr := make(chan error, 1)
	go func() { svcErr <- deploySvc.Start(ctx) }()

	// Agent handler
	agentHandler := api.NewAgentHandler(clusterStore, bus)

	// Auth components
	jwtSecret := []byte(cfg.JWTSecret)
	authenticator := auth.NewAdminKeyAuthenticator(cfg.AdminKey, auth.NewJWTAuthenticator(jwtSecret))
	authorizer := auth.NewRoleAuthorizer()

	// HTTP router
	router := api.NewRouter(api.RouterDeps{
		DeploySvc:     deploySvc,
		Orgs:          orgStore,
		Projects:      projectStore,
		Envs:          envStore,
		Services:      serviceStore,
		Clusters:      clusterStore,
		JWTSecret:     jwtSecret,
		SpecRegistry:  specRegistry,
		AgentHandler:  agentHandler,
		Authenticator: authenticator,
		Authorizer:    authorizer,
	})

	log.Printf("server listening on %s", cfg.ListenAddr)
	server := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: router,
	}

	go func() {
		select {
		case <-ctx.Done():
		case err := <-svcErr:
			if err != nil {
				log.Printf("deployment service: %v", err)
			}
		}
		server.Shutdown(context.Background())
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("listen: %w", err)
	}
	if err := <-svcErr; err != nil {
		return fmt.Errorf("deployment service: %w", err)
	}
	return nil
}
