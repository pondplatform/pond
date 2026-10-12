package server

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"

	_ "github.com/lib/pq"
	dbmigrate "github.com/pondplatform/pond/server/db"
	"github.com/pondplatform/pond/server/internal/api"
	"github.com/pondplatform/pond/server/internal/auth"
	"github.com/pondplatform/pond/server/internal/dependency"
	"github.com/pondplatform/pond/server/internal/events"
	"github.com/pondplatform/pond/server/internal/service"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/serviceconfig/config"
)

type Config struct {
	DatabaseURL    string
	ListenAddr     string
	ManagementAddr string
	RabbitMQURL    string
	// AdminKey grants unrestricted access to all API endpoints when set.
	// Configured via the POND_ADMIN_KEY environment variable.
	AdminKey  string
	JWTSecret string
}

func Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	log.Info("starting server", "addr", cfg.ListenAddr, "management_addr", cfg.ManagementAddr)

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

	log.Info("connecting to rabbitmq", "url", cfg.RabbitMQURL)
	topics, rmq, err := events.NewRabbitMQTopics(cfg.RabbitMQURL, log.WithGroup("rabbitmq"))
	if err != nil {
		return fmt.Errorf("rabbitmq: %w", err)
	}
	defer rmq.Close()
	log.Info("rabbitmq connected")

	handler, eventRouter := Build(db, topics, cfg, log)

	log.Info("starting deployment event router")
	svcErr := make(chan error, 1)
	go func() { svcErr <- eventRouter.Start(ctx) }()

	apiServer := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: handler,
	}

	mgmtServer := &http.Server{
		Addr: cfg.ManagementAddr,
		Handler: api.NewManagementRouter(map[string]api.HealthCheck{
			"postgres": db.PingContext,
			"rabbitmq": rmq.Check,
		}),
	}

	// Shutdown both servers when context is cancelled or deployment service exits.
	go func() {
		select {
		case <-ctx.Done():
			log.Info("shutdown signal received")
		case err := <-svcErr:
			if err != nil {
				log.Error("deployment service stopped", "err", err)
			}
		}
		apiServer.Shutdown(context.Background())
		mgmtServer.Shutdown(context.Background())
	}()

	log.Info("management server listening", "addr", cfg.ManagementAddr)
	go func() {
		if err := mgmtServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("management server error", "err", err)
		}
	}()

	log.Info("api server listening", "addr", cfg.ListenAddr)
	if err := apiServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("listen: %w", err)
	}
	if err := <-svcErr; err != nil {
		return fmt.Errorf("deployment service: %w", err)
	}
	return nil
}

// Build wires the API handler and the event router that must run alongside
// it.
func Build(db *sql.DB, topics events.Topics, cfg Config, log *slog.Logger) (http.Handler, *service.EventRouter) {
	repos := store.NewRepos(db)
	clusterStore := store.NewClusterStore(db)
	specRegistry := dependency.NewSpecRegistry()

	deploySvc, eventRouter := service.NewDeploymentService(
		repos, store.NewTransactor(db), specRegistry, config.NewResolver(),
		topics, log.WithGroup("deployment"),
	)
	agentHandler := api.NewAgentHandler(clusterStore, service.NewAgentConnectionService(topics), log.WithGroup("agent_handler"))

	jwtSecret := []byte(cfg.JWTSecret)
	handler := api.NewRouter(api.RouterDeps{
		DeploySvc:     deploySvc,
		Projects:      store.NewProjectStore(db),
		Envs:          repos.Envs,
		Services:      repos.Services,
		Clusters:      clusterStore,
		JWTSecret:     jwtSecret,
		SpecRegistry:  specRegistry,
		AgentHandler:  agentHandler,
		Authenticator: auth.NewAdminKeyAuthenticator(cfg.AdminKey, auth.NewJWTAuthenticator(jwtSecret)),
		Authorizer:    auth.NewRoleAuthorizer(),
		Log:           log.WithGroup("http"),
	})
	return handler, eventRouter
}
