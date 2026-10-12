//go:build integration

package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	shared "github.com/pondplatform/pond/shared/server/api"
)

// postJSON POSTs body to the harness and returns the status code and response
// body. A string body is sent verbatim, so a test can control key order;
// anything else is JSON-encoded.
func postJSON(ctx context.Context, t *testing.T, h *TestHarness, path string, body any) (int, string) {
	t.Helper()
	var raw []byte
	switch b := body.(type) {
	case nil:
	case string:
		raw = []byte(b)
	default:
		var err error
		if raw, err = json.Marshal(b); err != nil {
			t.Fatalf("marshal body for %s: %v", path, err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.BaseURL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("build request for %s: %v", path, err)
	}
	if raw != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+h.AdminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(respBody)
}

// submitDeployment submits req and returns the new deployment's ID.
func submitDeployment(ctx context.Context, t *testing.T, h *TestHarness, req shared.SubmitRequest) uuid.UUID {
	t.Helper()
	d, err := h.Client().SubmitDeployment(ctx, req)
	if err != nil {
		t.Fatalf("submit deployment: %v", err)
	}
	return d.ID
}

// configure POSTs dependency input for a deployment.
func configure(ctx context.Context, t *testing.T, h *TestHarness, id uuid.UUID, body any) (int, string) {
	t.Helper()
	return postJSON(ctx, t, h, fmt.Sprintf("/api/v1/deployments/%s/user-input", id), body)
}

// cancelDeployment POSTs a cancel for a deployment.
func cancelDeployment(ctx context.Context, t *testing.T, h *TestHarness, id uuid.UUID) (int, string) {
	t.Helper()
	return postJSON(ctx, t, h, fmt.Sprintf("/api/v1/deployments/%s/cancel", id), nil)
}

// getDeploymentJSON fetches a deployment as raw JSON, so assertions do not
// depend on response fields the client types may not have yet.
func getDeploymentJSON(ctx context.Context, t *testing.T, h *TestHarness, id uuid.UUID) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/v1/deployments/%s", h.BaseURL, id), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.AdminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET deployment %s: %v", id, err)
	}
	defer resp.Body.Close()
	var body map[string]any
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		body = map[string]any{"_raw": string(raw)}
	}
	return resp.StatusCode, body
}

// field returns body[key] as a string, or "" when it is absent or not a string.
func field(body map[string]any, key string) string {
	s, _ := body[key].(string)
	return s
}

// waitForTerminal polls the deployment until it is succeeded, failed or
// cancelled and returns the last body. On timeout it fails the test with the
// last state it saw, so a red run describes the bug.
func waitForTerminal(ctx context.Context, t *testing.T, h *TestHarness, id uuid.UUID, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var code int
	var body map[string]any
	for {
		code, body = getDeploymentJSON(ctx, t, h, id)
		if code == http.StatusOK {
			switch field(body, "status") {
			case "succeeded", "failed", "cancelled":
				return body
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("deployment did not reach a terminal state within %s (last GET: %d, status %q, error %q)\n%s",
				timeout, code, field(body, "status"), field(body, "error"), describeDeployment(h.DB, id))
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context done while waiting for deployment %s: %v", id, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// assertStatusHolds fails the test if the deployment leaves status at any
// point during d.
func assertStatusHolds(ctx context.Context, t *testing.T, h *TestHarness, id uuid.UUID, status string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		code, body := getDeploymentJSON(ctx, t, h, id)
		if code != http.StatusOK || field(body, "status") != status {
			t.Fatalf("deployment should stay %q, but GET returned %d with status %q (error %q)\n%s",
				status, code, field(body, "status"), field(body, "error"), describeDeployment(h.DB, id))
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context done while watching deployment %s: %v", id, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

type cmdRow struct {
	ID     uuid.UUID
	Type   string
	Status string
	Error  string
}

// commandsOf returns the deployment's commands in creation order.
func commandsOf(t *testing.T, db *sql.DB, id uuid.UUID) []cmdRow {
	t.Helper()
	rows, err := db.Query(`SELECT id, type, status, COALESCE(error, '') FROM commands WHERE deployment_id = $1 ORDER BY created_at`, id)
	if err != nil {
		t.Fatalf("query commands: %v", err)
	}
	defer rows.Close()
	var out []cmdRow
	for rows.Next() {
		var r cmdRow
		if err := rows.Scan(&r.ID, &r.Type, &r.Status, &r.Error); err != nil {
			t.Fatalf("scan command: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate commands: %v", err)
	}
	return out
}

// countCommands counts the deployment's commands of one type.
func countCommands(t *testing.T, db *sql.DB, id uuid.UUID, cmdType string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM commands WHERE deployment_id = $1 AND type = $2`, id, cmdType).Scan(&n); err != nil {
		t.Fatalf("count %s commands: %v", cmdType, err)
	}
	return n
}

// describeDeployment summarises the deployment, its dependency rows and its
// commands straight from the database, for failure messages. It reads the
// DB rather than the API because the API is one of the things under test.
func describeDeployment(db *sql.DB, id uuid.UUID) string {
	var b strings.Builder
	var status string
	var errMsg sql.NullString
	if err := db.QueryRow(`SELECT status, error FROM deployments WHERE id = $1`, id).Scan(&status, &errMsg); err != nil {
		fmt.Fprintf(&b, "  deployment %s: %v\n", id, err)
		return b.String()
	}
	fmt.Fprintf(&b, "  deployment %s: status=%s error=%q\n", id, status, errMsg.String)

	if rows, err := db.Query(`SELECT dependency_name, status, managed, command_id FROM dependency_deployments WHERE deployment_id = $1 ORDER BY dependency_name`, id); err == nil {
		for rows.Next() {
			var name, depStatus string
			var managed sql.NullBool
			var cmdID uuid.NullUUID
			if rows.Scan(&name, &depStatus, &managed, &cmdID) == nil {
				fmt.Fprintf(&b, "  dependency %s: status=%s managed=%v command_id=%v\n", name, depStatus, nullBool(managed), nullUUID(cmdID))
			}
		}
		rows.Close()
	}

	if rows, err := db.Query(`SELECT type, status, count(*) FROM commands WHERE deployment_id = $1 GROUP BY type, status ORDER BY type, status`, id); err == nil {
		fmt.Fprintf(&b, "  commands:")
		empty := true
		for rows.Next() {
			var cmdType, cmdStatus string
			var n int
			if rows.Scan(&cmdType, &cmdStatus, &n) == nil {
				fmt.Fprintf(&b, " %s/%s=%d", cmdType, cmdStatus, n)
				empty = false
			}
		}
		rows.Close()
		if empty {
			fmt.Fprintf(&b, " none")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func nullBool(v sql.NullBool) any {
	if !v.Valid {
		return "NULL"
	}
	return v.Bool
}

func nullUUID(v uuid.NullUUID) any {
	if !v.Valid {
		return "NULL"
	}
	return v.UUID
}

// failCommandInserts makes every insert of a cmdType command for clusterID
// fail with a database error, simulating a DB fault on that path. Other
// clusters, and so other tests, are unaffected. The returned func clears the
// fault; it is also cleared when the test ends.
func failCommandInserts(t *testing.T, db *sql.DB, clusterID uuid.UUID, cmdType string) (clear func()) {
	t.Helper()
	if _, err := db.Exec(`CREATE OR REPLACE FUNCTION pond_test_fail_command_insert() RETURNS trigger AS $$
BEGIN
	IF NEW.cluster_id::text = TG_ARGV[0] AND NEW.type = TG_ARGV[1] THEN
		RAISE EXCEPTION 'injected fault: % insert blocked', NEW.type;
	END IF;
	RETURN NEW;
END
$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create fault function: %v", err)
	}

	name := pq.QuoteIdentifier("pond_test_fail_" + strings.ReplaceAll(clusterID.String(), "-", "") + "_" + strings.ReplaceAll(cmdType, ".", "_"))
	if _, err := db.Exec(fmt.Sprintf(
		`CREATE TRIGGER %s BEFORE INSERT ON commands FOR EACH ROW EXECUTE FUNCTION pond_test_fail_command_insert(%s, %s)`,
		name, pq.QuoteLiteral(clusterID.String()), pq.QuoteLiteral(cmdType),
	)); err != nil {
		t.Fatalf("create fault trigger: %v", err)
	}
	clear = func() {
		if _, err := db.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON commands`, name)); err != nil {
			t.Errorf("drop fault trigger: %v", err)
		}
	}
	t.Cleanup(clear)
	return clear
}
