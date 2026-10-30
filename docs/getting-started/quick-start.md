# Quick Start

From a fresh VPS to a running app with HTTPS and a managed Postgres database in about five minutes.

## Prerequisites

- **A VPS** with a public IP, root SSH access, and ports `80`, `443` and `6443` open. 2 vCPU and 4 GB RAM is enough to start.
- **A domain name.** Cloudflare DNS is recommended (the free plan works): The homelab setup script sets up CertManager to get Let's Encrypt certificates for you from CloudFlare. With another DNS provider, bring your own cert-manager `ClusterIssuer` or use a self-signed CA (see [step 5](#5-install-pond)).
- **Local tools:** `kubectl`, `helm`, `git`, `make`, Go 1.25+, `curl`, `jq` and `openssl`. On macOS:
  ```sh
  brew install kubectl helm go jq
  ```

The examples use `example.com`. Replace it with your domain everywhere.

## 1. Point DNS at the VPS

Create an `A` records with your VPS's IP:

| Name | Used for |
|---|---|
| `*.example.com` | Your apps, e.g. `todo-app.example.com` |

On Cloudflare, set both to **DNS only** (grey cloud), not proxied.

Then create a Cloudflare API token (**My Profile → API Tokens → Create Custom Token**) with **Zone → DNS → Edit** and **Zone → Zone → Read** for your domain's zone. cert-manager uses it to prove you own the domain.

## 2. Install k3s on the VPS

```sh
ssh root@k3s.example.com
curl -sfL https://get.k3s.io | sh -s - server \
  --tls-san k3s.example.com
```

`--tls-san` adds the hostname to the API server's certificate, so `kubectl` on your machine accepts it. k3s already includes what Pond needs: storage, a load balancer, and traefik as the gateway.

## 3. Add the cluster to your kubeconfig

Still on the VPS, print the cluster's kubeconfig:

```sh
cat /etc/rancher/k3s/k3s.yaml
```

Copy the output into `~/.kube/config` on your machine. If that file already exists, back it up first. Then point `server:` at your domain, and replace every `default` with `pond`:

```yaml
clusters:
- cluster:
    certificate-authority-data: ...
    server: https://k3s.example.com:6443   # was https://127.0.0.1:6443
  name: pond                               # was default
contexts:
- context:
    cluster: pond                          # was default
    user: pond                             # was default
  name: pond                               # was default
current-context: pond                      # was default
users:
- name: pond                               # was default
  user:
    client-certificate-data: ...
    client-key-data: ...
```

Check that it works:

```sh
kubectl --context pond get nodes
```

## 4. Install the pond CLI

Prebuilt binaries aren't published yet, so build the CLI from source:

```sh
git clone https://github.com/pondplatform/pond.git
cd pond
make install-cli # ensure that ~/.local/bin/ is in your PATH
```

## 5. Install Pond

From the `pond` checkout:

```sh
./scripts/homelab-quick-setup.sh \
  --kube-context pond \
  --environments production \
  --features full \
  --domain example.com \
  --tls cloudflare
```

The script asks two questions: how app URLs should look (keep the default, `<service>.example.com`) and your Cloudflare API token. It shows its plan and changes nothing until you confirm. Then it:

- installs the Pond server and agent into the namespace `pond`
- installs CloudNativePG, so Pond can create Postgres databases for your apps
- turns on Gateway API in k3s's traefik and installs cert-manager, which gets a wildcard certificate for `*.example.com`
- creates a project `default` with an environment `production`
- adds a pond CLI context named `pond` and makes it active

The Pond server isn't exposed to the internet. The CLI reaches it through a `kubectl port-forward`, which it opens by itself. The admin credentials are saved in `~/.pond/setup-pond.env`.

Not using Cloudflare? Use `--tls self-signed` for a private CA, or `--tls issuer:<name>` for a cert-manager `ClusterIssuer` you've set up for your DNS provider. Leave out any flag and the script asks instead; `--help` lists them all.

## 6. Deploy your first app

The [postgres-todo](https://github.com/pondplatform/sample-apps/tree/main/postgres-todo) sample is a small Express app backed by Postgres. Its `pond.yml` declares the database and renders the connection details into a `.env` file:

```yaml
version: 1
name: todo-app
image: ghcr.io/pondplatform/sample-postgres-todo

gateway:
  http:
    enabled: true        # serve at https://todo-app.example.com

service:
  port: 3000

dependencies:
  myDb:
    type: postgres
    config:
      version: 17

configs:
  .env:
    format: .env
    mountPath: /usr/src/app/.env
    values:
      DB_HOST: "{{dependencies.myDb.host}}"
      DB_USER: "{{dependencies.myDb.username}}"
      DB_PASSWORD: "{{dependencies.myDb.password}}"
      DB_DATABASE: "{{dependencies.myDb.database}}"
      DB_PORT: "{{dependencies.myDb.port}}"
```

Next to it, `pond-input-production.json` tells Pond to create and manage the database in the `production` environment:

```json
{ "dependencies": { "myDb": { "managed": true, "values": { "instances": 1 } } } }
```

Deploy it:

```sh
cd ..
git clone https://github.com/pondplatform/sample-apps.git
cd sample-apps/postgres-todo
pond deploy --tag latest
```

Open `https://todo-app.example.com`. To see what's running:

```sh
pond deployment status
```

## Next steps

- **Deploy your own app:** run `pond init --image <your image>` in its directory, then see the [pond.yml reference](../services/pond-yml.md).
- **Something bigger:** [Lemmy](https://github.com/pondplatform/sample-apps/tree/main/lemmy) runs three services on one hostname, with a volume and shared secrets.
- **More samples:** [sample-apps](https://github.com/pondplatform/sample-apps) has Umami, n8n, Keycloak, Nextcloud, Forgejo and others.
- Read more about [dependencies](../services/dependencies.md), [config files](../services/config-files.md) and [per-environment settings](../services/environments.md).

## Good to know

- Pond keeps its own data in a Postgres pod without a volume. If that pod restarts, Pond forgets its projects, environments and tokens. Your apps keep running; re-run step 5 to set Pond up again.
- Re-running the setup script upgrades an existing install and keeps its secrets.
