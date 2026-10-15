# Config Files

The `configs` block lets you render configuration files from templates and mount them into your container at deploy time. Template variables are resolved from dependency output values, service settings, and environment variables.

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

A config file is mounted read-only and rendered again on every deploy. It can sit inside a [volume](volumes.md)'s directory, for an app that reads its config from the same directory it keeps its data in:

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
      DB_HOST: "{{db.host}}"
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
        host: "{{db.host}}"
        port: "{{db.port}}"
```

### `json`

Renders a JSON file:

```yaml
configs:
  config.json:
    format: json
    mountPath: /etc/myapp/config.json
    values:
      dbHost: "{{db.host}}"
      servicePort: "{{service.port}}"
```

---

## Template variables

String values support `{{variable}}` syntax at any depth, including inside nested maps and lists. Variables are resolved at deploy time.

| Variable | Description |
|---|---|
| `{{<depName>.host}}` | Output field from a dependency named `<depName>` |
| `{{<depName>.port}}` | Output field from a dependency named `<depName>` |
| `{{<depName>.<field>}}` | Any output field of a declared dependency |
| `{{service.port}}` | The `service.port` value from your `pond.yml` |
| `{{deployment.replicas}}` | The `deployment.replicas` value from your `pond.yml` |
| `{{env.<KEY>}}` | An environment variable declared in the `env` block |

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
      DB_HOST: "{{db.host}}"
      DB_PORT: "{{db.port}}"
      DB_USER: "{{db.username}}"
      DB_PASSWORD: "{{db.password}}"
      DB_NAME: "{{db.database}}"
      PORT: "{{service.port}}"
```

At deploy time, Pond resolves the `{{db.*}}` variables from the PostgreSQL dependency's output and mounts a rendered `/usr/src/app/.env` file into the container.
