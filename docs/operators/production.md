# Production Considerations

The default Helm chart configuration is optimised for local development. Before running Pond in production, review each of the following areas.

## TLS

The agent connects to the server using an unencrypted `ws://` WebSocket — TLS is not handled at the application layer. In production you must terminate TLS externally:

- **Ingress controller** (e.g. ingress-nginx + cert-manager): expose the server through an HTTPS ingress and configure the agent's `serverAddr` to point to the ingress hostname on port 443. The agent will need to be updated to use `wss://` — check the release notes for when WSS support is added.
- **Service mesh** (e.g. Istio, Linkerd): mTLS between pods handles in-cluster encryption automatically.

Until then, ensure agent-to-server traffic stays within a private network or is protected by a VPN.

## External PostgreSQL and RabbitMQ

The server Helm chart bundles single-replica PostgreSQL and RabbitMQ deployments **with no persistent volumes** — they are development-grade and will lose data on pod restart.

For production, provision your own PostgreSQL (e.g. CloudNative PG, RDS, Cloud SQL) and RabbitMQ (e.g. CloudAMQP, Amazon MQ) and supply their connection strings:

```sh
helm upgrade --install pond-server ... \
  --set databaseUrl="postgres://user:pass@db.prod.example.com:5432/pond?sslmode=require" \
  --set rabbitmqUrl="amqps://user:pass@mq.prod.example.com:5671/"
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

## Health and readiness

The server exposes health endpoints on the management port (`9090` by default):

| Endpoint | Description |
|---|---|
| `GET /health/live` | Always returns `200 {"status":"ok"}` |
| `GET /health/ready` | Returns `503` if PostgreSQL is unreachable |

These are already wired into the Helm chart's liveness and readiness probes.

## Prometheus metrics

The server exposes Go runtime metrics at `:9090/metrics`. The Helm chart adds Prometheus scrape annotations to the server pod:

```yaml
annotations:
  prometheus.io/scrape: "true"
  prometheus.io/port: "9090"
  prometheus.io/path: "/metrics"
```

If you use the Prometheus Operator, add a `ServiceMonitor` targeting the server's management port (`9090`).
