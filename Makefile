.PHONY: build build-images build-server-image build-agent-image build-cli-image build-todo-image \
       test test-integration e2e e2e-setup e2e-run e2e-teardown

# ---- Build targets ----

build:
	go build ./...

build-server-image:
	docker build -f infra/docker/Dockerfile.server -t pond-server:latest .

build-agent-image:
	docker build -f infra/docker/Dockerfile.agent -t pond-agent:latest .

build-cli-image:
	docker build -f infra/docker/Dockerfile.cli -t pond-cli:latest .

build-todo-image:
	docker build -t todo-app:latest ./test/test-data/CRUD-PostgreSQL-Todo-List/

build-images: build-server-image build-agent-image build-cli-image build-todo-image

# ---- Test targets ----

test:
	go test ./...

test-integration:
	go test -tags integration -v -timeout 120s ./internal/integration/...

