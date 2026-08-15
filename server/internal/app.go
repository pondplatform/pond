package server

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"

	_ "github.com/lib/pq"
	dbmigrate "github.com/pondplatform/pond/server/db"
	"github.com/pondplatform/pond/shared/serviceconfig/config"
	"github.com/pondplatform/pond/server/internal/api"
	"github.com/pondplatform/pond/server/internal/auth"
	"github.com/pondplatform/pond/server/internal/dependency"
	"github.com/pondplatform/pond/server/internal/events"
	"github.com/pondplatform/pond/server/internal/helmgen"
	"github.com/pondplatform/pond/server/internal/service"
	"github.com/pondplatform/pond/server/internal/store"
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

func Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	log.Info("starting server", "addr", cfg.ListenAddr)

	if cfg.JWTSecret == "" {
		return fmt.Errorf("POND_JWT_SECRET must be set")
	}

	log.Info("connecting to database", "url", cfg.DatabaseURL)
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}
	log.Info("database connected")

	log.Info("running database migrations")
	if err := dbmigrate.Run(db); err != nil {
		return fmt.Errorf("migrate db: %w", err)
	}
	log.Info("database migrations complete")

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

	log.Info("connecting to rabbitmq", "url", cfg.RabbitMQURL)
	bus, closeBus, err := events.NewRabbitMQBus(cfg.RabbitMQURL, log.WithGroup("rabbitmq"))
	if err != nil {
		return fmt.Errorf("rabbitmq: %w", err)
	}
	defer closeBus()
	log.Info("rabbitmq connected")

	// Services
	depSvc := service.NewDependencyService(specRegistry, envStore, deploymentInfoStore)
	helmGenerator := helmgen.NewGenerator()
	tmplRenderer := service.NewTemplateRenderer()
	resolver := config.NewResolver()
	deploySvc := service.NewDeploymentService(
		deploymentInfoStore, serviceStore, envStore,
		depSvc, helmGenerator, tmplRenderer, resolver,
		tx, bus, log.WithGroup("deployment"),
	)

	// Start the deployment service event loop (subscribes to all state-machine topics).
	log.Info("starting deployment service event loop")
	svcErr := make(chan error, 1)
	go func() { svcErr <- deploySvc.Start(ctx) }()

	// Agent handler
	agentConnSvc := service.NewAgentConnectionService(bus)
	agentHandler := api.NewAgentHandler(clusterStore, agentConnSvc, log.WithGroup("agent_handler"))

	// Auth components
	jwtSecret := []byte(cfg.JWTSecret)
	authenticator := auth.NewAdminKeyAuthenticator(cfg.AdminKey, auth.NewJWTAuthenticator(jwtSecret))
	authzRepo := store.NewAuthorizationStore(db)
	authorizer := auth.NewRoleAuthorizer(authzRepo)

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
		Log:           log.WithGroup("http"),
	})

	log.Info("server listening", "addr", cfg.ListenAddr)
	server := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: router,
	}

	go func() {
		select {
		case <-ctx.Done():
			log.Info("shutdown signal received")
		case err := <-svcErr:
			if err != nil {
				log.Error("deployment service stopped", "err", err)
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
