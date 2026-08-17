# Token Management

Pond uses two distinct authentication mechanisms: an admin key for bootstrap operations and JWT tokens for day-to-day API access.

## Admin key

The `POND_ADMIN_KEY` environment variable (Helm: `adminKey`) sets a static bearer token with unconditional admin access. It is intended only for initial bootstrapping (creating clusters, minting the first JWT tokens).

Use it sparingly and do not distribute it to end users.

## JWT tokens

All CLI and API clients should use JWT tokens. Tokens are created via the API using the admin key:

```sh
curl -s -X POST http://localhost:8080/api/v1/tokens \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"role":"admin","description":"ci-pipeline"}'
```

The response body contains the token:

```json
{"token": "eyJ..."}
```

**Roles:**

| Role | Description |
|---|---|
| `admin` | Full access to all resources |
| `member` | Can deploy and inspect services within assigned projects |
| `viewer` | Read-only access |

## Agent tokens

Each cluster has an agent token that the `pond-agent` uses to authenticate its WebSocket connection to the server. The token is only shown **once** when the cluster is created:

```sh
curl -s -X POST http://localhost:8080/api/v1/clusters \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name":"my-cluster"}'
# → {"id": "...", "agentToken": "<plain-token>"}
```

Pond stores only the SHA-256 hash of the token — the plain value is never stored and cannot be retrieved again.

## Rotating an agent token

If a token is lost or compromised, rotate it:

```sh
curl -s -X POST "http://localhost:8080/api/v1/clusters/$CLUSTER_ID/rotate-token" \
  -H "Authorization: Bearer $ADMIN_KEY"
# → {"agentToken": "<new-plain-token>"}
```

After rotation, reinstall the agent chart with the new token:

```sh
helm upgrade pond-agent oci://ghcr.io/pondplatform/charts/pond-agent \
  --namespace pond \
  --reuse-values \
  --set agentToken="<new-plain-token>"
```
