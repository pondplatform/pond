.PHONY: build build-images build-server-image build-agent-image build-cli \
        test test-integration vet verify \
        helm-lint \
        e2e-setup e2e-teardown e2e

build:
	go build ./shared/... ./cli/... ./agent/... ./server/...

build-server-image:
	docker build -f infra/docker/Dockerfile.server -t pond-server:latest .

build-agent-image:
	docker build -f infra/docker/Dockerfile.agent -t pond-agent:latest .

build-images: build-server-image build-agent-image

build-cli:
	go build -o ./build/pond ./cli/

docs:
	mdbook build

# ---- Test targets ----

test:
	go test ./shared/... ./cli/... ./agent/... ./server/...

test-integration:
	go test -tags integration -v -timeout 120s ./server/internal/integration/...

vet:
	go vet ./shared/... ./cli/... ./agent/... ./server/...

verify: vet test test-integration helm-lint

# ---- Helm targets ----

helm-lint:
	helm lint infra/deploy/helm/pond-server
	helm template ci infra/deploy/helm/pond-server >/dev/null
	helm package infra/deploy/helm/pond-server --destination /tmp
	helm lint infra/deploy/helm/pond-agent
	helm template ci infra/deploy/helm/pond-agent >/dev/null
	helm package infra/deploy/helm/pond-agent --destination /tmp
	helm lint infra/agent/helm-charts/base-service
	helm template ci infra/agent/helm-charts/base-service >/dev/null
	helm template ci infra/agent/helm-charts/base-service --set httpRoute.enabled=true >/dev/null
	helm template ci infra/agent/helm-charts/base-service --set httpRoute.enabled=true \
		--set 'httpRoute.rules[0].filters[0].type=RequestHeaderModifier' >/dev/null
	helm template ci infra/agent/helm-charts/base-service --set httpRoute.enabled=true \
		--set 'httpRoute.rules[0].matches[0].headers[0].name=x' \
		--set 'httpRoute.rules[0].matches[0].headers[0].value=yes' >/dev/null
	@# gateway-api rejects a rule carrying both a RequestRedirect filter and backendRefs.
	! helm template ci infra/agent/helm-charts/base-service --set httpRoute.enabled=true \
		--set 'httpRoute.rules[0].filters[0].type=RequestRedirect' | grep -q backendRefs
	@# Probes are opt-in: a chart default would be applied to every service that
	@# declares none, failing containers on a check they never asked for.
	! helm template ci infra/agent/helm-charts/base-service | grep -qE 'livenessProbe|readinessProbe'
	helm template ci infra/agent/helm-charts/base-service \
		--set 'readinessProbe.httpGet.path=/health/ready' \
		--set 'readinessProbe.httpGet.port=8080' >/dev/null

# ---- E2E targets ----

# build-cli, not build: the scenarios invoke ./build/pond, and a stale binary
# silently ignores pond.yml keys it does not know yet.
e2e-setup: build-cli
	./test/end-to-end/local-setup.sh

e2e-teardown:
	./test/end-to-end/teardown.sh

e2e: e2e-setup
	./test/end-to-end/deploy-postgres.sh
	./test/end-to-end/deploy-gateway.sh
