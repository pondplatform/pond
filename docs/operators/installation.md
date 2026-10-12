# Installation

This guide walks through installing the Pond server and connecting a cluster agent using Helm.

## Prerequisites

- A Kubernetes cluster with `kubectl` access
- Helm 3+
- `curl` and `jq` for bootstrapping via the API
- `openssl` for generating secrets

## 1. Generate secrets

```sh
JWT_SECRET=$(openssl rand -base64 32)
ADMIN_KEY=$(openssl rand -base64 24)
```

Keep these values — you will need them in subsequent steps and to reinstall.

## 2. Install the server

The `pond-server` Helm chart bundles PostgreSQL for development use. For production, provide an external instance instead (see [Production Considerations](production.md)).

Add the Helm repo and install:

```sh
helm upgrade --install pond-server oci://ghcr.io/pondplatform/charts/pond-server \
  --namespace pond \
  --create-namespace \
  --set jwtSecret="$JWT_SECRET" \
  --set adminKey="$ADMIN_KEY" \
  --wait
```

The server is exposed as a ClusterIP service on port `8080`. Port-forward it to verify:

```sh
kubectl port-forward -n pond svc/pond-server 8080:8080
```

```sh
curl http://localhost:8080/api/v1/clusters \
  -H "Authorization: Bearer $ADMIN_KEY"
# → {"items": []}
```

## 3. Register a cluster

Create a cluster entry in the server. The response includes the agent token — **save it, it is shown only once**.

```sh
CLUSTER_RESPONSE=$(curl -s -X POST http://localhost:8080/api/v1/clusters \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name":"my-cluster"}')

CLUSTER_ID=$(echo "$CLUSTER_RESPONSE" | jq -r '.id')
AGENT_TOKEN=$(echo "$CLUSTER_RESPONSE" | jq -r '.agentToken')
```

## 4. Install the agent

The `pond-agent` chart runs inside the same cluster it manages. It connects back to the server using an in-cluster service name.

```sh
helm upgrade --install pond-agent oci://ghcr.io/pondplatform/charts/pond-agent \
  --namespace pond \
  --set serverAddr="pond-server.pond.svc.cluster.local:8080" \
  --set agentToken="$AGENT_TOKEN" \
  --wait
```

## 5. Create a project and environment

```sh
# Mint a CLI token
TOKEN_RESPONSE=$(curl -s -X POST http://localhost:8080/api/v1/tokens \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"role":"admin","description":"initial-setup"}')
POND_TOKEN=$(echo "$TOKEN_RESPONSE" | jq -r '.token')

# Create a project
PROJECT_RESPONSE=$(curl -s -X POST http://localhost:8080/api/v1/projects \
  -H "Authorization: Bearer $POND_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"my-project"}')
PROJECT_ID=$(echo "$PROJECT_RESPONSE" | jq -r '.id')

# Create an environment (namespace must already exist)
kubectl create namespace staging
curl -s -X POST "http://localhost:8080/api/v1/projects/$PROJECT_ID/environments" \
  -H "Authorization: Bearer $POND_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"staging\",\"namespace\":\"staging\",\"clusterId\":\"$CLUSTER_ID\"}"
```

## 6. Configure the CLI

```sh
pond context add prod \
  --server  http://localhost:8080 \
  --token   "$POND_TOKEN" \
  --project "$PROJECT_ID" \
  --env     staging

pond context use prod
```

You are now ready to deploy services. See [Quick Start](../getting-started/quick-start.md).

---

## Gateway API for exposed services

Services that set `gateway.http.enabled` are exposed with an `HTTPRoute`, so the cluster must already implement Gateway API and provide a `Gateway` named `gateway` with a listener named `http`. See [Production Considerations](production.md) for the production shape. Pond installs neither the CRDs nor a controller.

### Rancher Desktop

Rancher Desktop ships the Gateway API CRDs and traefik, which implements them — but traefik's Gateway provider is off by default. Enable it once per cluster:

```sh
kubectl apply -f - <<'EOF'
apiVersion: helm.cattle.io/v1
kind: HelmChartConfig
metadata:
  name: traefik
  namespace: kube-system
spec:
  valuesContent: |-
    providers:
      kubernetesGateway:
        enabled: true
    gateway:
      enabled: false
EOF

kubectl get gatewayclass traefik
```

k3s's helm-controller redeploys traefik and adds a `traefik` GatewayClass. Setting `gateway.enabled: false` suppresses the chart's own default Gateway, since each Pond environment supplies its own.

One constraint: traefik binds a Gateway listener to whichever entryPoint already serves that port, so listeners must use `8000` — traefik's `web` entryPoint — rather than `80`, or the listener is rejected with `PortUnavailable`. Traefik's Service still publishes `:80`, so exposed services remain reachable at `http://<hostname>/`.

## Automated local setup

For local development with Rancher Desktop the repo includes a script that automates all of the above steps:

```sh
make e2e-setup
```

See `test/end-to-end/local-setup.sh` for details.

## Tear down

```sh
helm uninstall pond-server pond-agent -n pond
kubectl delete namespace pond
```
