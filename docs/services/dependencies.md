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

### `service`

Another Pond service of the same project, as deployed to the same environment. Its addresses are imported, so you don't hard-code `http://ollama:11434`.

**Config fields:**

| Field | Required | Description |
|---|---|---|
| `service` | yes | The `name` of the service to depend on. |

**Output fields:**

| Variable | Description |
|---|---|
| `{{dependencies.<name>.host}}` | The service's in-cluster DNS name, `<service>.<namespace>.svc.cluster.local` |
| `{{dependencies.<name>.port}}` | Its `service.port`, or 80 if it sets none |
| `{{dependencies.<name>.url}}` | `http://<host>:<port>` |
| `{{dependencies.<name>.gateway.hostname}}` | Its [`{{gateway.hostname}}`](config-files.md#template-variables). Only if it has a gateway route with a single hostname. |
| `{{dependencies.<name>.gateway.port}}` | Its `{{gateway.port}}`, under the same condition |
| `{{dependencies.<name>.gateway.url}}` | Its `{{gateway.url}}`, under the same condition |
| `{{dependencies.<name>.variables.<variable>}}` | The value of one of its [variables](pond-yml.md#variables) declared `visibility: public`. Not allowed in `command` or `gateway`: whether it is a secret isn't known until the deploy looks the service up. |

**Example:**

```yaml
name: open-webui
dependencies:
  llm:
    type: service
    config:
      service: ollama
env:
  OLLAMA_BASE_URL: "{{dependencies.llm.url}}"
```

These outputs are all you can import. A service's private variables, which variables are unless declared `visibility: public`, and its own dependencies' outputs stay its own. To share a key between two services, declare it public in the one and import it in the other:

```yaml
# lemmy-pictrs/pond.yml
variables:
  API_KEY: { secret: true, visibility: public }

# lemmy/pond.yml
dependencies:
  pictrs:
    type: service
    config:
      service: lemmy-pictrs
configs:
  config.hjson:
    values:
      pictrs:
        api_key: "{{dependencies.pictrs.variables.API_KEY}}"
``` The values come from its latest successful deployment to the environment, as that deployment rendered them, so:

- **Deploy it first.** A service that was never deployed to the environment, or was last deployed before Pond published these values, can't be depended on yet. The submit is rejected.
- **The values are copied when you deploy.** A later deployment of the service doesn't change yours. Redeploy yours to pick up, say, a changed port or a rotated key.

A `service` dependency takes no [input](#managed-vs-manual-dependencies); giving it some is an error.

---

## Managed vs. manual dependencies

Every dependency except a [`service`](#service) needs input that tells Pond whether to manage it for you or to connect to something that already exists. You give it with the deployment.

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
