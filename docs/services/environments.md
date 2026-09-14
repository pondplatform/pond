# Environment Overrides

The `overrides` block lets you tailor a service's configuration for specific environments without duplicating the entire `pond.yml`.

## How it works

When you deploy to a named environment (e.g. `production`), Pond looks for a matching key in `overrides` and merges it on top of the base configuration:

- **Struct fields** (`deployment`, `service`, `gateway`): individual fields are overridden; unset fields keep their base value.
- **Maps** (`env`, `dependencies`, `configs`): entries from the override are merged key-by-key. Override keys win; unrelated base keys are kept.
- **Lists** (`gateway.http.rules`): replaced wholesale, never appended. Routing rules are order-sensitive, so keeping the base rules in front would let them shadow the override. An explicit `rules: []` resets to the default catch-all route.

## Example

```yaml
version: 1
name: my-service
image: ghcr.io/example/my-service

deployment:
  replicas: 1

service:
  port: 8080

env:
  LOG_LEVEL: debug
  FEATURE_X: "false"

overrides:
  production:
    deployment:
      replicas: 3           # scale up; port stays 8080
    env:
      LOG_LEVEL: warn       # override; FEATURE_X is still "false"

  staging:
    env:
      FEATURE_X: "true"     # enable in staging only
```

When deploying to `production`:
- `deployment.replicas` → `3`
- `service.port` → `8080` (unchanged)
- `env.LOG_LEVEL` → `warn`
- `env.FEATURE_X` → `"false"` (unchanged)

When deploying to `staging`:
- `deployment.replicas` → `1` (unchanged)
- `env.LOG_LEVEL` → `debug` (unchanged)
- `env.FEATURE_X` → `"true"`

## What can be overridden

| Key | Type |
|---|---|
| `deployment` | `DeploymentSpec` — `replicas` |
| `service` | `ServiceSpec` — `port` |
| `gateway` | `GatewayConfig` — `http.enabled`, `http.hostname`, `http.rules` |
| `env` | map — merged key-by-key |
| `dependencies` | map — merged key-by-key |
| `configs` | map — merged key-by-key |

The `name`, `image`, `build`, and `management` fields cannot be overridden per environment.
