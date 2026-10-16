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
command: [serve, --port, "{{service.port}}"]

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
  PUBLIC_URL: "{{gateway.url}}"
  SIGNING_KEY: "{{variables.SIGNING_KEY}}"

variables:
  SIGNING_KEY: { secret: true }

dependencies:
  db:
    type: postgres

configs:
  .env:
    format: .env
    mountPath: /app/.env
    values:
      DB_HOST: "{{dependencies.db.host}}"
      DB_PORT: "{{dependencies.db.port}}"
      DB_USER: "{{dependencies.db.username}}"
      DB_PASSWORD: "{{dependencies.db.password}}"
      DB_NAME: "{{dependencies.db.database}}"

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
| `command` | list of strings | no | Replaces the image's default command (`CMD`); the image's entrypoint stays. |
| `deployment` | object | no | Workload settings: replica count, health probes and metrics. |
| `service` | object | no | Service port settings. |
| `gateway` | object | no | How the service is exposed to incoming HTTP traffic. Values can contain templates. |
| `dependencies` | map | no | What your app needs: databases, HTTP services, or other Pond services. See [Dependencies](dependencies.md). |
| `env` | map | no | Environment variables injected into the container. Values can contain templates. |
| `variables` | map | no | Values given with each deploy rather than in `pond.yml`, such as secrets. |
| `configs` | map | no | Config files rendered and mounted into the container. |
| `volumes` | map | no | Directories that keep their contents across restarts and redeploys. |
| `overrides` | map | no | Environment-specific overrides. Keys are environment names. |

### `command`

The arguments the container starts with. They replace the image's default command (its `CMD`) and are passed to the image's entrypoint, which stays: an image whose entrypoint prepares something before starting the app keeps doing so. Pond has no field to replace the entrypoint.

```yaml
command: [start, --optimized]
```

Each item is one argument, passed as written. There is no shell, so `$VAR`, `*` and quotes inside an argument reach the app unchanged. For shell features, run a shell, which works when the image's entrypoint runs whatever command it is given, as most official images' do:

```yaml
command: [sh, -c, 'exec app --id "$HOSTNAME"']
```

Arguments can contain [template variables](config-files.md#template-variables), such as `{{service.port}}` or `{{dependencies.db.host}}`, rendered at deploy time. Arguments are visible to anyone who can read the workload, so a dependency output is only allowed if its type declares it as not sensitive. `{{dependencies.db.password}}` is rejected, and so is any key an unmanaged dependency's input adds beyond the declared outputs, and any variable declared `secret: true`. Pass those in `env` or a config file instead.

A string such as `command: serve --port 8080` is rejected, because splitting it would need shell quoting rules. An empty list is rejected too: the image's default command can be replaced but not cleared, so leave `command` out to keep it.

An override replaces the whole list; see [Environment Overrides](environments.md).

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

#### Templates in the gateway

Every string under `gateway.http`, from the hostname to a header value, can contain [template variables](config-files.md#template-variables), rendered at deploy time:

```yaml
gateway:
  http:
    enabled: true
    hostname: "{{variables.CUSTOMER}}.example.com"
    rules:
      - matches:
          - path:
              value: "/{{environment.name}}"
        filters:
          - type: RequestHeaderModifier
            requestHeaderModifier:
              set:
                - name: X-Upstream
                  value: "{{dependencies.api.url}}"
```

- **Rendered values are checked like written ones.** With `CUSTOMER: Acme Corp`, the hostname above is rejected before anything is deployed. A value a dependency's output fills in is only known at rollout, so it's checked then, and a bad one fails the deployment.
- **`{{gateway.hostname}}` and `{{gateway.url}}` can't be used here**, because they come from the rendered gateway. They work in `env`, `configs` and `command`, so `PUBLIC_URL: "{{gateway.url}}"` gives `https://acme.example.com`.
- **No secrets.** Anyone who can read the service's `HTTPRoute` sees these values, so the values rejected in [`command`](#command) are rejected here too: a sensitive dependency output such as `{{dependencies.db.password}}`, any output the dependency's type doesn't declare, and any variable declared `secret: true`.
- **Only strings.** Numbers and booleans, such as `enabled` or a redirect's `statusCode`, can't contain templates.

### `dependencies`

A map of named dependencies. Each entry declares a dependency that must be provisioned before the service can start. See [Dependencies](dependencies.md) for all available types.

Names may contain letters, digits and underscores, because templates reference them as `{{dependencies.<name>.<output>}}`.

```yaml
dependencies:
  <name>:
    type: <dependency-type>
    config:          # optional, type-specific configuration
      key: value
```

### `env`

Environment variables set in the container. Values are strings and can contain [template variables](config-files.md#template-variables), rendered at deploy time.

```yaml
env:
  DATABASE_URL: "postgres://{{dependencies.db.username}}:{{dependencies.db.password}}@{{dependencies.db.host}}/{{dependencies.db.database}}"
  PUBLIC_URL: "{{gateway.url}}"
  LOG_LEVEL: debug
```

Pond keeps the rendered values in a Kubernetes Secret rather than in the workload, so a password in `env` isn't visible to everyone who can read the workload. Values reach your app as written: unlike in a Kubernetes manifest, `$(VAR)` isn't expanded. When a value changes, the service's pods restart to pick it up.

Templates can't read `env`. To use the same value in `env` and in a config file, declare it as a [variable](#variables).

### `variables`

Values that `pond.yml` leaves to each deploy, such as a signing key or a name that differs per customer. `pond.yml` declares them, and the deploy's input file gives their values. Reference them as `{{variables.<name>}}` in `env`, `configs`, `command` and `gateway`.

```yaml
variables:
  SIGNING_KEY: { secret: true }
  PUBLIC_NAME: {}
```

The values go under `variables:` in the input file, next to the dependency input. See [Dependencies](dependencies.md#managed-vs-manual-dependencies) for how `pond deploy` finds that file.

```yaml
# pond-input-staging.yml
variables:
  SIGNING_KEY: "s3cr3t"
  PUBLIC_NAME: "Acme"
```

| Field | Type | Default | Description |
|---|---|---|---|
| `secret` | boolean | `false` | Whether the value is a secret. A secret variable can't be used in `command` or `gateway`, which anyone who can read the workload sees. |
| `visibility` | `public` or `private` | `private` | Whether services that [depend on this one](dependencies.md#service) can import the value, as `{{dependencies.<name>.variables.<variable>}}`. A secret can be public too; it stays a secret where it's imported. |

- **Every declared variable needs a value in every deploy's input.** A missing value, or a value for a variable `pond.yml` doesn't declare, is rejected before anything is deployed. Values from earlier deployments are not reused.
- **Values are strings.** In a YAML input file a value keeps the text you wrote, so `PIN: 0123` stays `0123`. In JSON, write every value as a string.
- **Names** may contain letters, digits and underscores.
- **There are no overrides.** Each environment already has its own input file, `pond-input-<env>.yml`.
- **Storage:** Pond keeps the values with the deployment on the server, in plaintext, as it does an unmanaged dependency's input, and a public value also with each deployment that imports it. They are not returned by the API or written to the server log.

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
| `size` | string | yes | How much data you expect: a whole number of `Mi`, `Gi` or `Ti`. It can grow, see [Sizes](volumes.md#sizes). |

The name identifies the data, so renaming a volume gives the service a new, empty one. Names are lowercase letters, digits and `-`, at most 59 characters.

Every copy of the service mounts a `shared-single-node` volume. Each deploy stops every running copy before starting a new one, so deploys have a short downtime, and **a release that doesn't start leaves the service down**. Volumes can't be overridden per environment.

> **Warning:** Pond doesn't limit `deployment.replicas` for a service with volumes, but every replica reads and writes the same files at the same time. Most apps corrupt their data that way, and replicas on other nodes may not start at all. Read [What `shared-single-node` means](volumes.md#what-shared-single-node-means) before running more than one.

### `overrides`

Named blocks that are merged on top of the base config when deploying to a specific environment. See [Environment Overrides](environments.md).
