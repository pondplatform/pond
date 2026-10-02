package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// HealthCheck returns nil when the dependency it checks is healthy.
type HealthCheck func(ctx context.Context) error

// NewManagementRouter returns a handler for the management server (health + metrics).
// It runs on a separate port with no auth middleware. Liveness and readiness run
// the same checks, so a failing dependency both drains and restarts the pod.
func NewManagementRouter(checks map[string]HealthCheck) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/health/live", handleHealth(checks))
	r.GET("/health/ready", handleHealth(checks))
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	return r
}

func handleHealth(checks map[string]HealthCheck) gin.HandlerFunc {
	return func(c *gin.Context) {
		status, code := "ok", http.StatusOK
		results := make(map[string]string, len(checks))
		for name, check := range checks {
			if err := check(c.Request.Context()); err != nil {
				results[name] = err.Error()
				status, code = "unavailable", http.StatusServiceUnavailable
				continue
			}
			results[name] = "ok"
		}
		c.JSON(code, gin.H{"status": status, "checks": results})
	}
}
