# Quick Start

This guide walks you through deploying a simple todo-app service with Pond.

## Prerequisites

- A running Pond server (see [Installation](../operators/installation.md))
- A project and environment created on that server
- Your API token, project ID, and environment name

## 1. Install the CLI

> **Note:** A one-command installer is coming soon. For now, download the latest `pond` binary from the [GitHub releases page](https://github.com/pondplatform/pond/releases) and place it on your `$PATH`.

## 2. Configure a context

A context stores your server URL, token, project, and environment so you don't have to pass flags on every command.

```sh
pond context add prod \
  --server  https://pond.example.com \
  --token   <your-api-token> \
  --project <project-uuid> \
  --env     staging
```

Set it as the active context:

```sh
pond context use prod
```

## 3. Create a pond.yml

In your project directory, create a `pond.yml`:

```yaml
version: 1
name: todo-app
image: ghcr.io/example/todo-app

service:
  port: 8080

env:
  LOG_LEVEL: info
```

## 4. Deploy

```sh
pond deploy --tag v1.2.3
```

Pond returns a deployment ID immediately. To follow along until the deployment finishes:

```sh
pond deploy --tag v1.2.3 --wait
```

## 5. Check status

```sh
pond deployment status
```

Run from the service directory, this reads the service name from `pond.yml` and lists the latest deployment in every environment of the project. Pass a name (`pond deployment status <service>`) to inspect a service from anywhere else. Narrow it to one environment to see the deployment phase, each dependency, and any command logs:

```sh
pond deployment status <service> --env prod
```

A single deployment can also be inspected directly by ID with `pond deployment status --deployment-id <id>`.

## What's next

- Add a managed database — see [Dependencies](../services/dependencies.md)
- Mount configuration into your container — see [Config Files](../services/config-files.md)
- Use different settings per environment — see [Environment Overrides](../services/environments.md)
