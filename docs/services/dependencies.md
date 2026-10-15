# Dependencies

Pond can provision and connect dependencies for your service. You declare what your service needs in `pond.yml`; Pond provisions the dependency, collects its connection details, and makes them available as [template variables](config-files.md#template-variables) in your `env`, config files, `command` and `gateway`.

## Declaring a dependency

```yaml
dependencies:
  <name>:
    type: <type>
    config:          # optional, type-specific
      key: value
```

Template variables refer to the dependency by the `<name>` you choose: `{{dependencies.db.host}}` when the name is `db`.

---

## Built-in dependency types

### `postgres`

A PostgreSQL database.

**Config fields:**

| Field | Required | Description |
|---|---|---|
| `version` | no | PostgreSQL major version (e.g. `"16"`). |

**Output fields (available as template variables):**

| Variable | Description |
|---|---|
| `{{dependencies.<name>.host}}` | Database host |
| `{{dependencies.<name>.port}}` | Database port |
| `{{dependencies.<name>.username}}` | Database username |
| `{{dependencies.<name>.password}}` | Database password. Sensitive, so not allowed in [`command`](pond-yml.md#command). |
| `{{dependencies.<name>.database}}` | Database name |

**Example:**

```yaml
dependencies:
  db:
    type: postgres
```

---

### `http-service`

An external HTTP service that is not managed by Pond. Useful for referencing a third-party API or an internal service that you want to inject by URL rather than hardcoding it.

**Config fields:**

| Field | Required | Description |
|---|---|---|
| `url` | yes | The URL of the HTTP service. |

**Output fields:**

| Variable | Description |
|---|---|
| `{{dependencies.<name>.url}}` | The service URL |

**Example:**

```yaml
dependencies:
  auth:
    type: http-service
    config:
      url: https://auth.example.com
```

---

## Managed vs. manual dependencies

Every dependency needs input that tells Pond whether to manage it for you or to connect to something that already exists. You give it with the deployment.

Create an input file that tells Pond whether to manage the database for you, or connect to an existing one:

**Managed (Pond provisions the database):**

```json
{
  "dependencies": {
    "db": {
      "managed": true,
      "values": {
        "instances": 1
      }
    }
  }
}
```

**Unmanaged (you supply an existing database):**

```json
{
  "dependencies": {
    "db": {
      "managed": false,
      "values": {
        "host": "db.example.com",
        "port": "5432",
        "username": "myapp",
        "password": "secret",
        "database": "myapp_prod"
      }
    }
  }
}
```

Then pass it to `pond deploy`:

```sh
pond deploy --tag v1.2.3 --input ./deploy-input.json
```

Or name it `pond-input-<env>.json` (or `pond-input-<env>.yml`, with the same structure in YAML) next to `pond.yml`, and `pond deploy` picks it up for that environment without `--input`.

Every deployment needs input for every declared dependency; input from earlier deployments is not reused. A dependency the file leaves out makes the server reject the request, and nothing is created. Keeping the file as `pond-input-<env>.yml` next to `pond.yml` means each `pond deploy` sends it without extra flags.

The same file gives the values of the service's [variables](pond-yml.md#variables), under `variables:`:

```yaml
# pond-input-staging.yml
dependencies:
  db: { managed: true, values: { instances: 1 } }
variables:
  SIGNING_KEY: "s3cr3t"
  PUBLIC_NAME: "Acme"
```
