# Configuration Reference

Both the server and the agent are configured entirely via environment variables. When using the Helm charts, most variables are set automatically — refer to the corresponding `values.yaml` column to see which Helm value controls each variable.

## Server

| Variable | Helm value | Default | Required | Description |
|---|---|---|---|---|
| `POND_JWT_SECRET` | `jwtSecret` | — | **yes** | HMAC secret for signing and verifying JWT API tokens. The server refuses to start if this is absent. |
| `POND_ADMIN_KEY` | `adminKey` | — | no | Static bearer token with unconditional admin access. Useful for bootstrapping. |
| `DATABASE_URL` | `databaseUrl` | `postgres://pond:pond@postgres:5432/pond?sslmode=disable` | no | PostgreSQL connection string. |
| `RABBITMQ_URL` | `rabbitmqUrl` | `amqp://guest:guest@rabbitmq:5672/` | no | RabbitMQ connection string. |
| `LISTEN_ADDR` | `listenAddr` | `:8080` | no | Address for the main API HTTP server. |
| `MANAGEMENT_ADDR` | `managementAddr` | `:9090` | no | Address for the management server (health endpoints + Prometheus metrics). |
| `LOG_LEVEL` | — | `info` | no | Log verbosity: `debug`, `info`, `warn`, or `error`. |

Database migrations run automatically on startup — the process is idempotent.

## Agent

| Variable | Helm value | Default | Required | Description |
|---|---|---|---|---|
| `POND_AGENT_TOKEN` | `agentToken` | — | **yes** | Bearer token that authenticates this agent with the server. Obtain it when registering a cluster via the API. |
| `POND_SERVER_ADDR` | `serverAddr` | `localhost:8080` | no | `host:port` of the server. No scheme — plain TCP. |
| `LOG_LEVEL` | — | `info` | no | Log verbosity: `debug`, `info`, `warn`, or `error`. |

## Example: external PostgreSQL and RabbitMQ

Override the bundled instances with your own by passing the connection strings at install time:

```sh
helm upgrade --install pond-server oci://ghcr.io/pondplatform/charts/pond-server \
  --set jwtSecret="$JWT_SECRET" \
  --set adminKey="$ADMIN_KEY" \
  --set databaseUrl="postgres://myuser:mypass@db.example.com:5432/pond?sslmode=require" \
  --set rabbitmqUrl="amqp://myuser:mypass@mq.example.com:5672/"
```
