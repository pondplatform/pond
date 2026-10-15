# Config Files

The `configs` block lets you render configuration files from templates and mount them into your container at deploy time. Template variables are resolved from dependency outputs, service and environment settings, and [variables](pond-yml.md#variables).

## Basic structure

```yaml
configs:
  <name>:
    format: .env | yaml | json
    mountPath: /path/in/container/file
    values:
      KEY: "value or {{template}}"
```

Pond renders the file from `values`, formats it according to `format`, and mounts it at `mountPath` inside the container. `mountPath` is the full, absolute path of the file (including the file name), and each config must use a different `mountPath`. The `<name>` key only identifies the config.

A config file is mounted read-only and rendered again on every deploy. When its contents change, the service's pods restart to pick it up. It can sit inside a [volume](volumes.md)'s directory, for an app that reads its config from the same directory it keeps its data in:

```yaml
configs:
  configuration.yaml:
    format: yaml
    mountPath: /config/configuration.yaml
    values:
      default_config: {}

volumes:
  config:
    type: shared-single-node
    mountPath: /config
    size: 1Gi
```

The file covers that one path in the volume and leaves the rest of the volume writable. The container runtime leaves an empty placeholder at that path in the volume, which stays if you remove the config later. A volume can't be mounted at a config file's path or below it.

---

## Formats

### `.env`

Renders a `.env` file with keys in sorted order:

```
KEY='value'
OTHER_KEY='other $value'
```

Values are single-quoted, so dotenv readers take them literally: `$`, `"`, `\` and `#` reach your app unchanged. A value containing a single quote or a line break is double-quoted instead, with `\` → `\\`, `"` → `\"` and line breaks → `\n`. Readers differ on those escapes; Node's `dotenv`, for example, only decodes `\n` and `\r`.

```yaml
configs:
  .env:
    format: .env
    mountPath: /app/.env
    values:
      PORT: "8080"
      DB_HOST: "{{dependencies.db.host}}"
```

### `yaml`

Renders a YAML file:

```yaml
configs:
  config.yaml:
    format: yaml
    mountPath: /etc/myapp/config.yaml
    values:
      database:
        host: "{{dependencies.db.host}}"
        port: "{{dependencies.db.port}}"
```

### `json`

Renders a JSON file:

```yaml
configs:
  config.json:
    format: json
    mountPath: /etc/myapp/config.json
    values:
      dbHost: "{{dependencies.db.host}}"
      servicePort: "{{service.port}}"
```

---

## Template variables

String values support `{{variable}}` syntax at any depth, including inside nested maps and lists. Variables are resolved at deploy time.

| Variable | Value | Available when |
|---|---|---|
| `{{service.name}}` | The service's `name` | always |
| `{{service.port}}` | `service.port` | `service.port` is set |
| `{{deployment.replicas}}` | `deployment.replicas` | `deployment.replicas` is set |
| `{{environment.name}}` | The name of the environment being deployed to | always |
| `{{gateway.hostname}}` | `gateway.http.hostname` as rendered, or else `<name>.<environment base host>` | the gateway is enabled, and it has a hostname that isn't a `*.` wildcard |
| `{{gateway.url}}` | `https://<gateway.hostname>` | the same as `{{gateway.hostname}}` |
| `{{variables.<name>}}` | The input's value for a [variable](pond-yml.md#variables) | the variable is declared |
| `{{dependencies.<name>.<output>}}` | An output of a [dependency](dependencies.md), such as `{{dependencies.db.host}}` | the dependency is declared |

Templates work in four places:

- **`configs`:** every string value.
- **[`env`](pond-yml.md#env):** every value.
- **[`command`](pond-yml.md#command):** every argument, except values that may be secret: a dependency output marked sensitive, such as `{{dependencies.db.password}}`, any output the dependency's type doesn't declare, and a variable declared `secret: true`. Anyone who can read the workload sees its arguments, so pass these in `env` or a config file instead.
- **[`gateway`](pond-yml.md#templates-in-the-gateway):** every string, from the hostname to a header value, except the values that may be secret, as in `command`, and `{{gateway.hostname}}` and `{{gateway.url}}`, which come from the rendered gateway.

A template can't read `env`, so there is no `{{env.<NAME>}}`. To use the same value in `env` and in a config file, declare it as a variable.

Pond renders each value once. A value that itself contains `{{…}}`, such as a variable's, reaches your app as written.

A reference that can never resolve is rejected before anything is deployed, for example an undeclared dependency, or `{{gateway.url}}` without the gateway. A dependency's outputs are only known once it's provisioned, so a reference to an output it doesn't have fails the rollout instead. The same goes for the gateway's rendered values, which are checked like written ones: before anything is deployed, or at rollout where a dependency's output fills them in.

---

## Full example

`pond.yml`:

```yaml
version: 1
name: postgres-demo
image: ghcr.io/example/postgres-demo

service:
  port: 3000

dependencies:
  db:
    type: postgres

configs:
  .env:
    format: .env
    mountPath: /usr/src/app/.env
    values:
      DB_HOST: "{{dependencies.db.host}}"
      DB_PORT: "{{dependencies.db.port}}"
      DB_USER: "{{dependencies.db.username}}"
      DB_PASSWORD: "{{dependencies.db.password}}"
      DB_NAME: "{{dependencies.db.database}}"
      PORT: "{{service.port}}"
```

At deploy time, Pond resolves the `{{dependencies.db.*}}` variables from the PostgreSQL dependency's output and mounts a rendered `/usr/src/app/.env` file into the container.
