# pond.yml Reference

Every service that Pond deploys is described by a `pond.yml` file in the root of your project directory.

## Minimal example

```yaml
version: 1
name: my-service
image: ghcr.io/example/my-service

service:
  port: 8080
```

## Full example

```yaml
version: 1
name: my-service
image: ghcr.io/example/my-service

service:
  port: 8080
  replicas: 2

ingress:
  enabled: true

management:
  health:
    port: 8080
    endpoint: /healthz
  metrics:
    port: 9090
    endpoint: /metrics

env:
  LOG_LEVEL: info
  FEATURE_X: "true"

dependencies:
  db:
    type: postgres

configs:
  .env:
    format: .env
    mountDir: /app
    values:
      DB_HOST: "{{db.host}}"
      DB_PORT: "{{db.port}}"
      DB_USER: "{{db.username}}"
      DB_PASSWORD: "{{db.password}}"
      DB_NAME: "{{db.database}}"

overrides:
  production:
    service:
      replicas: 3
    env:
      LOG_LEVEL: warn
```

---

## Field reference

### Top-level fields

| Field | Type | Required | Description |
|---|---|---|---|
| `version` | integer | yes | Schema version. Must be `1`. |
| `name` | string | yes | Service name. Used as the Helm release name. |
| `image` | string | yes* | Docker image reference (without tag). Required unless `build` is set. |
| `build` | string | no | Path to a Dockerfile relative to `pond.yml`. Used to tell Pond which image to build; the built image is pushed and referenced automatically. |
| `ingress` | object | no | Ingress settings. |
| `service` | object | no | Service port and replica settings. |
| `management` | object | no | Health check and metrics endpoints. |
| `dependencies` | map | no | External services your app needs (databases, HTTP services). |
| `env` | map | no | Environment variables injected into the container. |
| `configs` | map | no | Config files rendered and mounted into the container. |
| `overrides` | map | no | Environment-specific overrides. Keys are environment names. |

### `ingress`

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | boolean | `false` | Whether to create a Kubernetes Ingress for this service. |

### `service`

| Field | Type | Default | Description |
|---|---|---|---|
| `port` | integer | — | Container port the application listens on. |
| `replicas` | integer | `1` | Number of pod replicas. |

### `management`

Describes your application's internal health and metrics endpoints. Pond uses these to configure Kubernetes liveness/readiness probes and Prometheus scraping.

| Field | Type | Description |
|---|---|---|
| `health.port` | integer | Port the health endpoint is served on. |
| `health.endpoint` | string | Path of the health endpoint (e.g. `/healthz`). |
| `metrics.port` | integer | Port the metrics endpoint is served on. |
| `metrics.endpoint` | string | Path of the metrics endpoint (e.g. `/metrics`). |

### `dependencies`

A map of named dependencies. Each entry declares a dependency that must be provisioned before the service can start. See [Dependencies](dependencies.md) for all available types.

```yaml
dependencies:
  <name>:
    type: <dependency-type>
    config:          # optional, type-specific configuration
      key: value
```

### `env`

Plain key/value pairs injected as environment variables. Values are strings.

```yaml
env:
  DATABASE_URL: postgres://...
  LOG_LEVEL: debug
```

### `configs`

A map of config files to render and mount into the container. See [Config Files](config-files.md) for the full template syntax.

```yaml
configs:
  <filename>:
    format: .env | yaml | json
    mountDir: /path/in/container
    values:
      KEY: "static or {{templated}} value"
```

### `overrides`

Named blocks that are merged on top of the base config when deploying to a specific environment. See [Environment Overrides](environments.md).
