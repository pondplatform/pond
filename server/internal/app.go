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
	Sweep     service.SweepConfig
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

	handler, background := Build(db, topics, cfg, log)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	log.Info("starting background work")
	backgroundErr := make(chan error, 1)
	go func() {
		backgroundErr <- background(ctx)
		cancel()
	}()

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

	// Shut both servers down on a signal, or when background work stops.
	go func() {
		<-ctx.Done()
		log.Info("shutting down")
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
	cancel()
	if err := <-backgroundErr; err != nil {
		return fmt.Errorf("background work: %w", err)
	}
	return nil
}

// Build wires the API handler and the background work that must run
// alongside it until its context is cancelled.
func Build(db *sql.DB, topics events.Topics, cfg Config, log *slog.Logger) (http.Handler, func(context.Context) error) {
	repos := store.NewRepos(db)
	clusterStore := store.NewClusterStore(db)
	specRegistry := dependency.NewSpecRegistry()

	deploySvc, eventRouter, sweeper := service.NewDeploymentService(
		repos, store.NewTransactor(db), specRegistry, config.NewResolver(),
		topics, cfg.Sweep, log.WithGroup("deployment"),
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
	background := func(ctx context.Context) error {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		swept := make(chan struct{})
		go func() {
			defer close(swept)
			sweeper.Run(ctx)
		}()
		err := eventRouter.Start(ctx)
		cancel()
		<-swept
		return err
	}
	return handler, background
}
