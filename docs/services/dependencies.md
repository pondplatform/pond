# Dependencies

Pond can provision and connect dependencies for your service. You declare what your service needs in `pond.yml`; Pond provisions the dependency, collects its connection details, and makes them available as template variables in your [config files](config-files.md).

## Declaring a dependency

```yaml
dependencies:
  <name>:
    type: <type>
    config:          # optional, type-specific
      key: value
```

The `<name>` you choose becomes the prefix for template variables (e.g. `{{db.host}}` when the name is `db`).

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
| `{{<name>.host}}` | Database host |
| `{{<name>.port}}` | Database port |
| `{{<name>.username}}` | Database username |
| `{{<name>.password}}` | Database password |
| `{{<name>.database}}` | Database name |

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
| `{{<name>.url}}` | The service URL |

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

When you deploy a service with a `postgres` dependency, Pond checks whether it needs input from you to proceed. The deployment enters the `awaiting_input` state and the CLI will print:

```
Deployment is awaiting input. Run:
  pond deployment configure --deployment-id <id> --file <path>
```

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

Then submit the input:

```sh
pond deployment configure \
  --deployment-id <id> \
  --file ./deploy-input.json
```

The deployment resumes automatically after receiving the input.
