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


# ---- Install Local ---

install-cli: build-cli
	cp ./build/pond ~/.local/bin/pond

# ---- Test targets ----

test:
	go test ./shared/... ./cli/... ./agent/... ./server/...

test-integration:
	go test -tags integration -v -timeout 15m ./server/internal/integration/...

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
	@# RollingUpdate renders its parameters so helm owns them and a later switch
	@# to Recreate removes them; Kubernetes rejects them on a Recreate strategy.
	helm template ci infra/agent/helm-charts/base-service | grep -q 'maxUnavailable: 25%'
	! helm template ci infra/agent/helm-charts/base-service --set strategy.type=Recreate | grep -q rollingUpdate
	@# Without args the image's CMD runs; numbers stay strings, which Kubernetes requires.
	! helm template ci infra/agent/helm-charts/base-service -s templates/deployment.yaml | grep -q 'args:'
	helm template ci infra/agent/helm-charts/base-service -s templates/deployment.yaml \
		--set 'args[0]=serve' --set-string 'args[1]=8080' | grep -A2 'args:' | grep -q '"8080"'
	! helm template ci infra/agent/helm-charts/base-service | grep -q PersistentVolumeClaim
	helm lint infra/agent/helm-charts/base-service --set strategy.type=Recreate \
		--set 'persistentVolumeClaims[0].name=data' --set 'persistentVolumeClaims[0].size=1Gi'
	@# A claim is named after the release, survives removal, and leaves the
	@# storage class to the cluster default.
	helm template ci infra/agent/helm-charts/base-service \
		--set 'persistentVolumeClaims[0].name=data' --set 'persistentVolumeClaims[0].size=1Gi' \
		| grep -A8 'kind: PersistentVolumeClaim' | grep -q 'name: ci-base-service-data'
	helm template ci infra/agent/helm-charts/base-service \
		--set 'persistentVolumeClaims[0].name=data' --set 'persistentVolumeClaims[0].size=1Gi' \
		| grep -q 'helm.sh/resource-policy: keep'
	! helm template ci infra/agent/helm-charts/base-service \
		--set 'persistentVolumeClaims[0].name=data' --set 'persistentVolumeClaims[0].size=1Gi' \
		| grep -q storageClassName

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
	./test/end-to-end/deploy-volumes.sh
	./test/end-to-end/deploy-command.sh
