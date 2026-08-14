# pond

Pond is a deployment platform. The server manages deployments; agents run inside customer clusters and execute Helm/Tofu commands; the CLI is the user-facing interface.

## Structure

| Module | Description |
|---|---|
| `server/` | API server and deployment orchestration |
| `agent/` | Cluster agent — connects to server via WebSocket |
| `cli/` | `pond` CLI |
| `shared/` | Types and wire protocol shared across modules |
| `infra/` | Docker images, Helm charts, and Terraform |

## Prerequisites

- Go 1.25+
- Docker
- Helm
- Rancher Desktop with a local Kubernetes cluster

## Common tasks

```
make build            # compile all modules
make build-cli        # build ./bin/pond binary
make build-images     # build all three Docker images
make test             # unit tests
make test-integration # integration tests (uses testcontainers, no infra needed)
make vet              # go vet
make verify           # vet + unit + integration + helm lint (full pre-push check)
make helm-lint        # lint, render, and package both Helm charts
```

## Local development

All development is done against a local Kubernetes cluster (Rancher Desktop).

```sh
make e2e-setup        # install server + agent via Helm, bootstrap org/cluster/tokens
make e2e              # run deploy-simple and deploy-postgres smoke tests
make e2e-teardown     # clean up everything
```

Credentials written by `e2e-setup` land in `test/end-to-end/.e2e-env`.

## Releasing

Push a `v*` tag. The release workflow builds and pushes Docker images to GHCR and publishes Helm charts as OCI artifacts.
