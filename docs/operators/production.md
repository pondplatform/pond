# Production Considerations

The default Helm chart configuration is optimised for local development. Before running Pond in production, review each of the following areas.

## TLS

The agent connects to the server using an unencrypted `ws://` WebSocket — TLS is not handled at the application layer. In production you must terminate TLS externally:

- **Ingress controller** (e.g. ingress-nginx + cert-manager): expose the server through an HTTPS ingress and configure the agent's `serverAddr` to point to the ingress hostname on port 443. The agent will need to be updated to use `wss://` — check the release notes for when WSS support is added.
- **Service mesh** (e.g. Istio, Linkerd): mTLS between pods handles in-cluster encryption automatically.

Until then, ensure agent-to-server traffic stays within a private network or is protected by a VPN.

## Gateway for exposed services

Services that set `gateway.http.enabled` are exposed with a Gateway API `HTTPRoute`, not an Ingress. Each attached cluster must therefore provide:

- The [Gateway API CRDs](https://gateway-api.sigs.k8s.io/guides/) and a controller that implements them.
- A `Gateway` named `gateway` with a listener named `http`, in the namespace the service is deployed into. This is what routes attach to by default.

TLS is terminated by the Gateway listener rather than per service, so that listener needs a certificate covering the environment's ingress base host — typically a wildcard for `*.<base host>`, since Pond derives each service's hostname as `<name>.<base host>`. If you want HTTP traffic redirected to HTTPS, configure that on the Gateway; there is no per-route equivalent.

Pond doesn't read the Gateway, so tell it how clients reach the listener: an environment's `gatewayTls` (default `true`) and `gatewayPort` (default 443 with TLS, 80 without) decide the scheme and port of `{{gateway.url}}`, and of the `gateway.*` values other services [import](../services/dependencies.md#service). Set them when creating the environment, or later with `PATCH /api/v1/environments/<id>`; a `gatewayPort` of `0` resets it to the default.

```json
{ "gatewayTls": false, "gatewayPort": 8080 }
```

## External PostgreSQL

The server keeps all of its state in PostgreSQL, including the queue of commands waiting for an agent. The server Helm chart bundles a single-replica PostgreSQL deployment **with no persistent volume** — it is development-grade and will lose data on pod restart.

For production, provision your own PostgreSQL (e.g. CloudNative PG, RDS, Cloud SQL) and supply its connection string:

```sh
helm upgrade --install pond-server ... \
  --set databaseUrl="postgres://user:pass@db.prod.example.com:5432/pond?sslmode=require"
```

## Agent RBAC

The `pond-agent` ServiceAccount is bound to a `ClusterRole` with `apiGroups: ["*"] resources: ["*"] verbs: ["*"]` — effectively cluster-admin. This is required because the agent runs Helm charts and OpenTofu modules on behalf of arbitrary services, which may create any Kubernetes resource.

Be aware of this when deciding which clusters to attach to a Pond server. Treat the agent token with the same level of care as a cluster-admin kubeconfig.

## Agent state persistence

OpenTofu state files are stored on a PersistentVolumeClaim mounted at `/states` inside the agent pod. Ensure your cluster provides a `StorageClass` that supports `ReadWriteOnce` with durable backing storage. Configure the storage class and size in the agent Helm values:

```yaml
persistence:
  storageClass: "standard"
  size: 10Gi
```

## Storage for service volumes

Services that declare [`volumes`](../services/volumes.md) get one `ReadWriteOnce` PersistentVolumeClaim per volume, from the cluster's **default** StorageClass. Make sure the default is durable, and allows volume expansion if services should be able to grow their volumes. Pond doesn't back volumes up; that is the cluster's job.

Pond doesn't limit the replicas of a service with volumes, and every replica mounts the same claim. What happens to replicas on other nodes depends on the kind of storage. With block storage they never start (`Multi-Attach`). With node-local storage they are all scheduled onto the node holding the data. With network file storage they usually run anywhere. Tell your teams which kind each environment uses, since they need it to judge whether [several replicas](../services/volumes.md#more-than-one-replica) are safe.

Claims are never deleted by Pond. They stay when a volume is removed from `pond.yml`, so a team can add it back and keep its data. Delete a claim by hand once the data is no longer needed.

When upgrading Pond, **upgrade the agents before the server**: an agent from before volumes can't create the claims, and deploys of services with volumes fail until it is upgraded.

## Health and readiness

The server exposes health endpoints on the management port (`9090` by default):

| Endpoint | Description |
|---|---|
| `GET /health/live` | Returns `503` if PostgreSQL is unreachable |
| `GET /health/ready` | Same checks as `/health/live` |

Both endpoints report each check by name:

```json
{"status":"unavailable","checks":{"postgres":"dial tcp 10.0.0.12:5432: connect: connection refused"}}
```

These are already wired into the Helm chart's liveness and readiness probes.
Because PostgreSQL is part of the liveness check, a PostgreSQL outage restarts the
server, and it crash-loops until PostgreSQL is reachable again.

## Prometheus metrics

The server exposes Go runtime metrics at `:9090/metrics`. The Helm chart adds Prometheus scrape annotations to the server pod:

```yaml
annotations:
  prometheus.io/scrape: "true"
  prometheus.io/port: "9090"
  prometheus.io/path: "/metrics"
```

If you use the Prometheus Operator, add a `ServiceMonitor` targeting the server's management port (`9090`).
