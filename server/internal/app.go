package server

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	_ "github.com/lib/pq"
	dbmigrate "github.com/pondplatform/pond/server/db"
	"github.com/pondplatform/pond/server/internal/api"
	"github.com/pondplatform/pond/server/internal/auth"
	"github.com/pondplatform/pond/server/internal/dependency"
	"github.com/pondplatform/pond/server/internal/service"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/serviceconfig/config"
)

type Config struct {
	DatabaseURL    string
	ListenAddr     string
	ManagementAddr string
	// AdminKey grants unrestricted access to all API endpoints when set.
	// Configured via the POND_ADMIN_KEY environment variable.
	AdminKey  string
	JWTSecret string
	// StopCheckInterval is how often an agent connection checks whether the
	// command its agent runs should be cancelled. Zero means 2s.
	StopCheckInterval time.Duration
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

	apiServer := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: Build(db, cfg, log),
	}

	mgmtServer := &http.Server{
		Addr: cfg.ManagementAddr,
		Handler: api.NewManagementRouter(map[string]api.HealthCheck{
			"postgres": db.PingContext,
		}),
	}

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
	return nil
}

// Build wires the API handler.
func Build(db *sql.DB, cfg Config, log *slog.Logger) http.Handler {
	repos := store.NewRepos(db)
	specRegistry := dependency.NewSpecRegistry()

	deploySvc, agents := service.NewDeploymentService(
		repos, store.NewTransactor(db), specRegistry, config.NewResolver(), log.WithGroup("deployment"),
	)

	stopCheck := cfg.StopCheckInterval
	if stopCheck <= 0 {
		stopCheck = 2 * time.Second
	}

	jwtSecret := []byte(cfg.JWTSecret)
	return api.NewRouter(api.RouterDeps{
		DeploySvc:     deploySvc,
		Projects:      store.NewProjectStore(db),
		Envs:          repos.Envs,
		Services:      repos.Services,
		Deployments:   repos.Deployments,
		Clusters:      repos.Clusters,
		JWTSecret:     jwtSecret,
		SpecRegistry:  specRegistry,
		AgentHandler:  api.NewAgentHandler(repos.Clusters, agents, stopCheck, log.WithGroup("agent_handler")),
		Authenticator: auth.NewAdminKeyAuthenticator(cfg.AdminKey, auth.NewJWTAuthenticator(jwtSecret)),
		Authorizer:    auth.NewRoleAuthorizer(),
		Log:           log.WithGroup("http"),
	})
}
