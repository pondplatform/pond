//go:build integration

package api

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/pondplatform/pond/server/internal/events"
)

func TestManagementHealth_RabbitMQConnectionClosed(t *testing.T) {
	_, rmq, err := events.NewRabbitMQTopics(startRabbitMQ(t), slog.Default())
	if err != nil {
		t.Fatalf("new rabbitmq topics: %v", err)
	}
	t.Cleanup(rmq.Close)

	h := NewManagementRouter(map[string]HealthCheck{"postgres": passing, "rabbitmq": rmq.Check})

	for _, path := range []string{"/health/live", "/health/ready"} {
		if code, body := getHealth(t, h, path); code != http.StatusOK {
			t.Fatalf("%s while connected: status = %d, body = %+v, want 200", path, code, body)
		}
	}

	rmq.Close()

	for _, path := range []string{"/health/live", "/health/ready"} {
		code, body := getHealth(t, h, path)
		if code != http.StatusServiceUnavailable {
			t.Errorf("%s after close: status = %d, want 503", path, code)
		}
		if body.Checks["rabbitmq"] != "connection closed" {
			t.Errorf("%s after close: checks[rabbitmq] = %q, want %q", path, body.Checks["rabbitmq"], "connection closed")
		}
		if body.Checks["postgres"] != "ok" {
			t.Errorf("%s after close: checks[postgres] = %q, want ok", path, body.Checks["postgres"])
		}
	}
}
