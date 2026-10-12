package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type healthBody struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

func getHealth(t *testing.T, h http.Handler, path string) (int, healthBody) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body healthBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: decode body %q: %v", path, rec.Body.String(), err)
	}
	return rec.Code, body
}

func passing(context.Context) error { return nil }

func TestManagementHealth_AllChecksPass(t *testing.T) {
	h := NewManagementRouter(map[string]HealthCheck{"postgres": passing, "other": passing})

	for _, path := range []string{"/health/live", "/health/ready"} {
		code, body := getHealth(t, h, path)
		if code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, code)
		}
		if body.Status != "ok" {
			t.Errorf("%s: body status = %q, want ok", path, body.Status)
		}
		for _, name := range []string{"postgres", "other"} {
			if body.Checks[name] != "ok" {
				t.Errorf("%s: checks[%s] = %q, want ok", path, name, body.Checks[name])
			}
		}
	}
}

func TestManagementHealth_FailingCheckReturns503(t *testing.T) {
	failing := func(context.Context) error { return errors.New("connection refused") }

	for _, failed := range []string{"postgres", "other"} {
		checks := map[string]HealthCheck{"postgres": passing, "other": passing}
		checks[failed] = failing
		h := NewManagementRouter(checks)

		for _, path := range []string{"/health/live", "/health/ready"} {
			code, body := getHealth(t, h, path)
			if code != http.StatusServiceUnavailable {
				t.Errorf("%s with %s down: status = %d, want 503", path, failed, code)
			}
			if body.Status != "unavailable" {
				t.Errorf("%s with %s down: body status = %q, want unavailable", path, failed, body.Status)
			}
			for name, result := range body.Checks {
				want := "ok"
				if name == failed {
					want = "connection refused"
				}
				if result != want {
					t.Errorf("%s with %s down: checks[%s] = %q, want %q", path, failed, name, result, want)
				}
			}
		}
	}
}
