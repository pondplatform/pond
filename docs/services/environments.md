# Environment Overrides

The `overrides` block lets you tailor a service's configuration for specific environments without duplicating the entire `pond.yml`.

## How it works

When you deploy to a named environment (e.g. `production`), Pond looks for a matching key in `overrides` and merges it on top of the base configuration:

- **Struct fields** (`deployment`, `service`, `gateway`): individual fields are overridden; unset fields keep their base value.
- **Maps** (`env`, `dependencies`, `configs`): entries from the override are merged key-by-key. Override keys win; unrelated base keys are kept.
- **Lists** (`command`, `gateway.http.rules`): replaced wholesale, never appended. Arguments are positional, and routing rules are order-sensitive, so keeping the base rules in front would let them shadow the override. An explicit `rules: []` resets to the default catch-all route.

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
| `command` | list — replaced as a whole |
| `deployment` | `DeploymentSpec` — `replicas`, `management` |
| `service` | `ServiceSpec` — `port` |
| `gateway` | `GatewayConfig` — `http.enabled`, `http.hostname`, `http.rules` |
| `env` | map — merged key-by-key |
| `dependencies` | map — merged key-by-key |
| `configs` | map — merged key-by-key |

The `name`, `image` and `build` fields cannot be overridden per environment.

Neither can `volumes`: a service needs the same storage in staging as in production, just as it needs the same memory. Each environment still gets its own volume with its own data. An `overrides.<env>.volumes` key is rejected like any other unknown field.

> **Warning:** an override that raises `deployment.replicas` for a service with volumes makes every replica in that environment read and write the same files. Pond accepts it, but it is only safe for some apps, on some storage. Read [More than one replica](volumes.md#more-than-one-replica) first, and check it for each environment you raise it in.

Within `deployment.management`, each of `livenessProbe`, `readinessProbe` and
`metrics` is replaced as a whole when the override names it, rather than merged
field by field — a probe assembled half from the base and half from the override
is rarely what you want. Sub-blocks the override leaves out keep their base
values:

```yaml
deployment:
  management:
    livenessProbe:
      httpGet:
        path: /health/live
      periodSeconds: 15
    readinessProbe:
      httpGet:
        path: /health/ready

overrides:
  production:
    deployment:
      management:
        # Replaces the base liveness probe entirely, so periodSeconds falls back
        # to the Kubernetes default of 10 rather than being inherited as 15.
        livenessProbe:
          httpGet:
            path: /health/live
          failureThreshold: 6
        # readinessProbe is not named here, so it keeps /health/ready.
```
