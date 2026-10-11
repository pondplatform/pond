//go:build integration

package integration

// Known bugs in request validation. Each test asserts the correct behaviour,
// so it fails until its fix lands; the doc comment names the PR that fixes
// it. Do not skip these or weaken them to match today's behaviour.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestConfigure_IsTransactional checks that a rejected configure request
// writes nothing. The body has valid input for postgres, then input for an
// undeclared dependency. A handler that writes as it goes updates postgres
// before it reaches the unknown name. It walks the inputs in map order,
// which is random, so one request may hit the unknown name first and write
// nothing; ten deployments make a false pass very unlikely.
// Fixed by PR 4.
func TestConfigure_IsTransactional(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr, testAMQPURL)
	scenario := BuildScenario(ctx, t, h)

	// Sent verbatim so postgres comes first, which is also the map's first
	// insertion and so its likeliest iteration start.
	const body = `{"dependencies": {
		"postgres": {"managed": true, "values": {"instances": 1}},
		"nope": {"managed": true, "values": {}}
	}}`

	const attempts = 10
	var ids []uuid.UUID
	for i := range attempts {
		id := submitDeployment(ctx, t, h, SubmitRequest(scenario, ServiceConfigWithDep("test-service"), "v1.0.0"))
		code, resp := configure(ctx, t, h, id, body)
		if code < 400 || code >= 500 {
			t.Errorf("configure %d: expected a 4xx rejection for the undeclared dependency, got %d: %s", i, code, resp)
		}
		ids = append(ids, id)
	}

	written := 0
	for _, id := range ids {
		var status string
		var managed sql.NullBool
		if err := h.DB.QueryRowContext(ctx,
			`SELECT status, managed FROM dependency_deployments WHERE deployment_id = $1 AND dependency_name = 'postgres'`,
			id,
		).Scan(&status, &managed); err != nil {
			t.Fatalf("query postgres dependency of %s: %v", id, err)
		}
		if status != "awaiting_input" || managed.Valid {
			written++
			t.Logf("deployment %s: rejected configure left postgres status=%s managed=%v", id, status, nullBool(managed))
		}
	}
	if written > 0 {
		t.Errorf("%d of %d rejected configure requests wrote the postgres dependency row; expected none", written, attempts)
	}
}
