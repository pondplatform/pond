.PHONY: build build-images build-server-image build-agent-image build-cli-image build-todo-image \
       test test-integration

# ---- Build targets ----

build:
	go build ./shared/... ./cli/... ./agent/... ./server/...

build-server-image:
	docker build -f infra/docker/Dockerfile.server -t pond-server:latest .

build-agent-image:
	docker build -f infra/docker/Dockerfile.agent -t pond-agent:latest .

build-cli-image:
	docker build -f infra/docker/Dockerfile.cli -t pond-cli:latest .

build-todo-image:
	docker build -t todo-app:latest ./test/test-data/CRUD-PostgreSQL-Todo-List/

build-images: build-server-image build-agent-image build-cli-image build-todo-image

build-cli:
	go build -o ./bin/pond ./cli/

# ---- Test targets ----

test:
	go test ./shared/... ./cli/... ./agent/... ./server/...

test-integration:
	go test -tags integration -v -timeout 120s ./server/internal/integration/...
