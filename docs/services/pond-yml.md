# pond.yml Reference

Every service that Pond deploys is described by a `pond.yml` file in the root of your project directory.

Keys not listed in this reference are rejected with the file and line, e.g. `pond.yml:4: unknown field "replica"`. Free-form maps (`env`, `configs.*.values` and `dependencies.*.config`) accept any keys.

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

deployment:
  replicas: 1          # with volumes, read the warnings in volumes.md before raising this
  management:
    livenessProbe:
      httpGet:
        path: /health/live
      initialDelaySeconds: 5
      periodSeconds: 15
    readinessProbe:
      httpGet:
        path: /health/ready
      periodSeconds: 10
    metrics:
      port: 9090
      endpoint: /metrics

service:
  port: 8080

gateway:
  http:
    enabled: true
    hostname: my-service.example.com
    rules:
      - matches:
          - path:
              type: PathPrefix
              value: /api
        filters:
          - type: URLRewrite
            urlRewrite:
              path:
                type: ReplacePrefixMatch
                replacePrefixMatch: /

env:
  LOG_LEVEL: info
  FEATURE_X: "true"

dependencies:
  db:
    type: postgres

configs:
  .env:
    format: .env
    mountPath: /app/.env
    values:
      DB_HOST: "{{db.host}}"
      DB_PORT: "{{db.port}}"
      DB_USER: "{{db.username}}"
      DB_PASSWORD: "{{db.password}}"
      DB_NAME: "{{db.database}}"

volumes:
  uploads:
    type: shared-single-node
    mountPath: /app/uploads
    size: 10Gi

overrides:
  production:
    env:
      LOG_LEVEL: warn
```

---

## Field reference

### Top-level fields

| Field | Type | Required | Description |
|---|---|---|---|
| `version` | integer | yes | Schema version. Must be `1`. |
| `name` | string | yes | Service name. Used as the Helm release name, so it must be lowercase alphanumeric or `-`, start and end alphanumeric, and be at most 53 characters. |
| `image` | string | yes* | Docker image reference (without tag). Required unless `build` is set. |
| `build` | string | no | Path to a Dockerfile relative to `pond.yml`. Used to tell Pond which image to build; the built image is pushed and referenced automatically. |
| `deployment` | object | no | Workload settings: replica count, health probes and metrics. |
| `service` | object | no | Service port settings. |
| `gateway` | object | no | How the service is exposed to incoming HTTP traffic. |
| `dependencies` | map | no | External services your app needs (databases, HTTP services). |
| `env` | map | no | Environment variables injected into the container. |
| `configs` | map | no | Config files rendered and mounted into the container. |
| `volumes` | map | no | Directories that keep their contents across restarts and redeploys. |
| `overrides` | map | no | Environment-specific overrides. Keys are environment names. |

### `deployment`

| Field | Type | Default | Description |
|---|---|---|---|
| `replicas` | integer | `1` | Number of pod replicas. With `volumes`, every replica reads and writes the same files: read [More than one replica](volumes.md#more-than-one-replica) before going above `1`. |
| `management` | object | none | Health probes and metrics endpoint. See below. |

#### `deployment.management`

| Field | Type | Default | Description |
|---|---|---|---|
| `livenessProbe` | object | none | Restarts the container when the check fails. |
| `readinessProbe` | object | none | Removes the pod from the Service until the check passes. |
| `metrics.port` | integer | `service.port` | Port the metrics endpoint is served on. |
| `metrics.endpoint` | string | none | Path of the metrics endpoint (e.g. `/metrics`). Setting it adds `prometheus.io/*` scrape annotations to the pod. |

**Probes are opt-in.** A probe you do not declare is not configured at all — Pond
never guesses an endpoint on your behalf, because a wrong guess turns a healthy
container into a restart loop. Declare only the probes your app actually serves.

Probe entries follow the upstream
[`Probe`](https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/pod-v1/#Probe)
shape, so examples from the Kubernetes docs can be pasted in directly.

| Field | Type | Default | Description |
|---|---|---|---|
| `httpGet.path` | string | required | Path to request, must start with `/`. |
| `httpGet.port` | integer | `service.port` | Port to request. |
| `httpGet.scheme` | string | `HTTP` | `HTTP` or `HTTPS`. |
| `httpGet.httpHeaders` | list | none | Extra request headers, each `{name, value}`. |
| `initialDelaySeconds` | integer | `0` | Delay before the first check. |
| `periodSeconds` | integer | `10` | How often to check. |
| `timeoutSeconds` | integer | `1` | Per-check timeout. |
| `successThreshold` | integer | `1` | Consecutive successes needed to pass. Must be `1` for `livenessProbe`. |
| `failureThreshold` | integer | `3` | Consecutive failures before acting. |

Only `httpGet` is supported: `exec`, `tcpSocket` and `grpc` checks are
deliberately not settable, so a check is always a plain HTTP request Pond can
reason about. `startupProbe` is not supported either — use
`initialDelaySeconds` or a higher `failureThreshold` on the liveness probe for a
slow-starting app.

```yaml
deployment:
  management:
    readinessProbe:
      httpGet:
        path: /health/ready
        port: 8081
      periodSeconds: 5
      failureThreshold: 6
```

### `service`

| Field | Type | Default | Description |
|---|---|---|---|
| `port` | integer | `8080` | Container port the application listens on. |

### `gateway`

Exposes the service to HTTP traffic by creating a [Gateway API](https://gateway-api.sigs.k8s.io/) `HTTPRoute`. Only HTTP is supported today, hence the `http` nesting.

| Field | Type | Default | Description |
|---|---|---|---|
| `http.enabled` | boolean | `false` | Whether to create an `HTTPRoute` for this service. |
| `http.hostname` | string | `<name>.<environment base host>` | Hostname the route matches. |
| `http.rules` | list | a single catch-all `PathPrefix: /` rule | Routing rules. See below. |

`http.rules` entries follow the upstream [`HTTPRouteRule`](https://gateway-api.sigs.k8s.io/reference/spec/#httprouterule) shape, so examples from the Gateway API docs can be pasted in directly. Both `matches` (on `path`, `headers`, `queryParams` and `method`) and `filters` (all six types, including `RequestHeaderModifier`, `URLRewrite` and `RequestRedirect`) are supported.

`parentRefs` and `backendRefs` are deliberately not settable: Pond attaches the route to the environment's Gateway and points it at this service. TLS is terminated by the Gateway listener rather than per route.

The one exception is a rule containing a `RequestRedirect` filter. Gateway API forbids combining a redirect with a backend, so Pond omits the backend for that rule — the redirect is answered by the Gateway and the request never reaches your service.

```yaml
gateway:
  http:
    enabled: true
    rules:
      - matches:
          - path:
              type: PathPrefix
              value: /admin
          - headers:
              - name: X-Internal
                value: "true"
        filters:
          - type: RequestHeaderModifier
            requestHeaderModifier:
              set:
                - name: X-Forwarded-Prefix
                  value: /admin
```

### `dependencies`

A map of named dependencies. Each entry declares a dependency that must be provisioned before the service can start. See [Dependencies](dependencies.md) for all available types.

Names may contain letters, digits and underscores, because config files reference them as `{{<name>.<field>}}`.

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

A map of config files to render and mount into the container. `mountPath` is the full path of the file inside the container. See [Config Files](config-files.md) for the full template syntax.

```yaml
configs:
  <name>:
    format: .env | yaml | json
    mountPath: /path/in/container/file
    values:
      KEY: "static or {{templated}} value"
```

### `volumes`

A map of directories that keep their contents across restarts and redeploys. See [Volumes](volumes.md) for how a service with volumes is deployed.

```yaml
volumes:
  <name>:
    type: shared-single-node
    mountPath: /path/in/container
    size: 10Gi
```

| Field | Type | Required | Description |
|---|---|---|---|
| `type` | string | yes | Which copies of the service mount the volume, and from where. Only `shared-single-node` so far: one volume that every copy of the service mounts, attached to one node at a time. |
| `mountPath` | string | yes | Absolute path of the directory inside the container. Volumes may be nested, and config files may be mounted inside a volume, but a volume can't be mounted at or below a config file. |
| `size` | string | yes | How much data you expect: a whole number of `Mi`, `Gi` or `Ti`. It can grow, but never shrink. |

The name identifies the data, so renaming a volume gives the service a new, empty one. Names are lowercase letters, digits and `-`, at most 59 characters.

Every copy of the service mounts a `shared-single-node` volume. Each deploy stops every running copy before starting a new one, so deploys have a short downtime, and **a release that doesn't start leaves the service down**. Volumes can't be overridden per environment.

> **Warning:** Pond doesn't limit `deployment.replicas` for a service with volumes, but every replica reads and writes the same files at the same time. Most apps corrupt their data that way, and replicas on other nodes may not start at all. Read [What `shared-single-node` means](volumes.md#what-shared-single-node-means) before running more than one.

### `overrides`

Named blocks that are merged on top of the base config when deploying to a specific environment. See [Environment Overrides](environments.md).
