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

# ---- E2E targets ----

KIND_CLUSTER_NAME := pond-e2e
DOCKER_NETWORK := pond-e2e

e2e: e2e-setup e2e-run e2e-teardown

e2e-setup: build-images
	# Start server + postgres
	docker compose up -d --wait
	# Create Kind cluster
	kind create cluster --name $(KIND_CLUSTER_NAME) --config e2e/kind-config.yaml
	# Connect Kind node to docker-compose network for agent-to-server connectivity
	docker network connect $(DOCKER_NETWORK) $$(docker ps --filter "name=$(KIND_CLUSTER_NAME)-control-plane" -q) || true
	# Load images into Kind
	kind load docker-image pond-agent:latest --name $(KIND_CLUSTER_NAME)
	kind load docker-image todo-app:latest --name $(KIND_CLUSTER_NAME)
	# Deploy agent via Helm
	helm install pond-agent deploy/helm/pond-agent \
		--set serverAddr=pond-server:8080 \
		--set agentToken=e2e-test-token \
		--kube-context kind-$(KIND_CLUSTER_NAME) \
		--namespace pond-system --create-namespace
	# Wait for agent pod to be ready
	kubectl --context kind-$(KIND_CLUSTER_NAME) -n pond-system wait --for=condition=ready pod -l app=pond-agent --timeout=60s

e2e-run:
	bash e2e/run-tests.sh

e2e-teardown:
	kind delete cluster --name $(KIND_CLUSTER_NAME) || true
	docker compose down -v || true
