#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="kind-pond-e2e"
SERVER_URL="http://localhost:8080"

# Deterministic UUIDs from db/seed.sql
PROJECT_ID="00000000-0000-0000-0000-000000000002"
ENV_ID="00000000-0000-0000-0000-000000000010"
SERVICE_NAME="todo-app"

PASSED=0
FAILED=0
SKIPPED=0

pass() { echo "  PASS: $1"; PASSED=$((PASSED + 1)); }
fail() { echo "  FAIL: $1"; FAILED=$((FAILED + 1)); }
skip() { echo "  SKIP: $1"; SKIPPED=$((SKIPPED + 1)); }

echo "=== Tier 1: Server API Smoke Tests ==="

echo "Waiting for server..."
for i in $(seq 1 30); do
    if curl -sf "${SERVER_URL}/projects/${PROJECT_ID}/environments" >/dev/null 2>&1; then
        echo "Server is ready."
        break
    fi
    if [ "$i" -eq 30 ]; then
        echo "Server did not become ready in time."
        exit 1
    fi
    sleep 1
done

echo "Test: List environments..."
ENV_RESPONSE=$(curl -sf "${SERVER_URL}/projects/${PROJECT_ID}/environments")
if echo "$ENV_RESPONSE" | jq -e '.' >/dev/null 2>&1; then
    pass "List environments returns valid JSON"
else
    fail "List environments did not return valid JSON"
fi

echo "Test: Submit deployment..."
DEPLOY_RESPONSE=$(curl -sf -X POST "${SERVER_URL}/deployments" \
    -H 'Content-Type: application/json' \
    -d "{
        \"ProjectID\": \"${PROJECT_ID}\",
        \"EnvironmentID\": \"${ENV_ID}\",
        \"ServiceConfig\": {
            \"version\": 1,
            \"name\": \"${SERVICE_NAME}\",
            \"image\": \"todo-app\",
            \"service\": {\"port\": 3000, \"replicas\": 1},
            \"ingress\": {\"enabled\": false}
        },
        \"ImageTag\": \"latest\",
        \"TriggeredBy\": \"e2e-test\"
    }" 2>&1) || true

if [ -n "$DEPLOY_RESPONSE" ]; then
    DEPLOY_ID=$(echo "$DEPLOY_RESPONSE" | jq -r '.ID // .id // empty')
    if [ -n "$DEPLOY_ID" ]; then
        pass "Submit deployment (id: ${DEPLOY_ID})"

        echo "Test: Get deployment status..."
        STATUS_RESPONSE=$(curl -sf "${SERVER_URL}/deployments/${DEPLOY_ID}")
        STATUS=$(echo "$STATUS_RESPONSE" | jq -r '.Status // .status // empty')
        if [ "$STATUS" = "pending" ]; then
            pass "Deployment status is pending"
        else
            fail "Deployment status expected 'pending', got '${STATUS}'"
        fi
    else
        fail "Submit deployment (no ID in response: ${DEPLOY_RESPONSE})"
    fi
else
    fail "Submit deployment (empty response)"
fi

echo ""
echo "=== Tier 2: Agent Infrastructure ==="

echo "Test: Agent pod running in Kind..."
AGENT_STATUS=$(kubectl --context "$KUBE_CONTEXT" -n pond-system get pods -l app=pond-agent -o jsonpath='{.items[0].status.phase}' 2>/dev/null) || true
if [ "$AGENT_STATUS" = "Running" ]; then
    pass "Agent pod is Running"
else
    fail "Agent pod status: ${AGENT_STATUS:-not found}"
fi

echo "Test: Helm available in agent container..."
if kubectl --context "$KUBE_CONTEXT" -n pond-system exec deploy/pond-agent -- helm version --short >/dev/null 2>&1; then
    pass "Helm is available in agent"
else
    fail "Helm not available in agent container"
fi

echo ""
echo "=== Tier 3: Full Deployment Flow ==="
skip "Agent-server gRPC connection not yet implemented (see internal/agent/connection.go)"

echo ""
echo "=== Results ==="
echo "Passed: ${PASSED}  Failed: ${FAILED}  Skipped: ${SKIPPED}"

if [ "$FAILED" -gt 0 ]; then
    exit 1
fi
