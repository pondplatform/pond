# Config Files

The `configs` block lets you render configuration files from templates and mount them into your container at deploy time. Template variables are resolved from dependency output values, service settings, and environment variables.

## Basic structure

```yaml
configs:
  <filename>:
    format: .env | yaml | json
    mountDir: /path/in/container
    values:
      KEY: "value or {{template}}"
```

Pond renders the file from `values`, formats it according to `format`, and mounts it at `<mountDir>/<filename>` inside the container.

---

## Formats

### `.env`

Renders a shell-style `.env` file:

```
KEY=value
OTHER_KEY=other_value
```

```yaml
configs:
  .env:
    format: .env
    mountDir: /app
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
    mountDir: /etc/myapp
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
    mountDir: /etc/myapp
    values:
      dbHost: "{{db.host}}"
      servicePort: "{{service.port}}"
```

---

## Template variables

Values support `{{variable}}` syntax. Variables are resolved at deploy time.

| Variable | Description |
|---|---|
| `{{<depName>.host}}` | Output field from a dependency named `<depName>` |
| `{{<depName>.port}}` | Output field from a dependency named `<depName>` |
| `{{<depName>.<field>}}` | Any output field of a declared dependency |
| `{{service.port}}` | The `service.port` value from your `pond.yml` |
| `{{service.replicas}}` | The `service.replicas` value from your `pond.yml` |
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
    mountDir: /usr/src/app
    values:
      DB_HOST: "{{db.host}}"
      DB_PORT: "{{db.port}}"
      DB_USER: "{{db.username}}"
      DB_PASSWORD: "{{db.password}}"
      DB_NAME: "{{db.database}}"
      PORT: "{{service.port}}"
```

At deploy time, Pond resolves the `{{db.*}}` variables from the PostgreSQL dependency's output and mounts a rendered `/usr/src/app/.env` file into the container.
