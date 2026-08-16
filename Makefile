.PHONY: build build-images build-server-image build-agent-image build-cli \
        test test-integration vet verify \
        helm-lint \
        e2e-setup e2e-teardown e2e

# ---- Build targets ----

build:
	go build ./shared/... ./cli/... ./agent/... ./server/...

build-server-image:
	docker build -f infra/docker/Dockerfile.server -t pond-server:latest .

build-agent-image:
	docker build -f infra/docker/Dockerfile.agent -t pond-agent:latest .

build-images: build-server-image build-agent-image

build-cli:
	go build -o ./bin/pond ./cli/

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

# ---- E2E targets ----

e2e-setup: build
	./test/end-to-end/local-setup.sh

e2e-teardown:
	./test/end-to-end/teardown.sh

e2e: e2e-setup
	./test/end-to-end/deploy-postgres.sh
