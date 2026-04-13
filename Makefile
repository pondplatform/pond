.PHONY: build build-images build-server-image build-agent-image build-cli-image build-todo-image \
       e2e e2e-setup e2e-run e2e-teardown

# ---- Build targets ----

build:
	go build ./...

build-server-image:
	docker build -f docker/Dockerfile.server -t pond-server:latest .

build-agent-image:
	docker build -f docker/Dockerfile.agent -t pond-agent:latest .

build-cli-image:
	docker build -f docker/Dockerfile.cli -t pond-cli:latest .

build-todo-image:
	docker build -t todo-app:latest ./test-data/CRUD-PostgreSQL-Todo-List/

build-images: build-server-image build-agent-image build-cli-image build-todo-image

