# Baseline — build, test, and deploy targets.

.DEFAULT_GOAL := help

GO        ?= go
CHART     := deploy/charts/baseline

# Raspberry Pi k3s cluster (shares the finances cluster's registry + context).
PI_REGISTRY ?= <REGISTRY_HOST>:5000
PI_CONTEXT  ?= k3s
PI_TAG      ?= $(shell git rev-parse --short HEAD)
IMAGE       := $(PI_REGISTRY)/baseline

.PHONY: help
help: ## List targets
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

## --- Local dev ---------------------------------------------------------

.PHONY: build
build: ## Build the binary to ./bin/baseline
	$(GO) build -o ./bin/baseline ./cmd/baseline

.PHONY: test
test: ## Unit tests only (no Docker)
	$(GO) test -short ./...

.PHONY: test-all
test-all: ## Full suite (needs Docker for the pgvector testcontainer)
	$(GO) test ./...

.PHONY: vet
vet: ## go vet
	$(GO) vet ./...

.PHONY: dev-setup
dev-setup: ## Start local pgvector + build + seed (see scripts/dev-setup.sh)
	./scripts/dev-setup.sh

## --- Helm --------------------------------------------------------------

.PHONY: helm-deps
helm-deps: ## Fetch chart dependencies (Bitnami Postgres subchart)
	helm dependency build $(CHART)

.PHONY: helm-lint
helm-lint: helm-deps ## Lint the chart
	helm lint $(CHART)

.PHONY: helm-template
helm-template: ## Render the chart with the Pi overlay
	helm template baseline $(CHART) -f deploy/pi/values.yaml --set-string image.tag=$(PI_TAG)

## --- Local Docker Desktop Kubernetes ----------------------------------
# Runs the full stack (Baseline + Mem0 + Neo4j) on Docker Desktop's k8s, with
# Ollama running NATIVELY on the Mac (GPU) reached via host.docker.internal.
# Images are built locally and imported into the node's containerd (no registry).

K8S_NODE       ?= desktop-control-plane
LOCAL_CONTEXT  ?= docker-desktop

.PHONY: local-images
local-images: ## Build the 4 images (incl. the read-only UI) and load them into Docker Desktop's node
	docker build -t baseline:dev .
	docker build -t baseline-postgresql:16-pgvector deploy/postgres
	docker build --build-arg PATCH_OLLAMA=1 -t baseline-mem0-api:ollama deploy/mem0-api
	docker build -t baseline-ui:dev frontend
	@for img in baseline:dev baseline-postgresql:16-pgvector baseline-mem0-api:ollama baseline-ui:dev; do \
	  echo "loading $$img into $(K8S_NODE)..."; \
	  docker save "$$img" | docker exec -i $(K8S_NODE) ctr -n k8s.io images import - >/dev/null; \
	done

.PHONY: local-up
local-up: local-images helm-deps ## Build+load images and install the chart on Docker Desktop
	@command -v ollama >/dev/null && ollama list 2>/dev/null | grep -q qwen2.5:3b || \
	  echo "WARN: host Ollama / qwen2.5:3b not found — run: ollama serve; ollama pull qwen2.5:3b; ollama pull nomic-embed-text"
	helm upgrade --install baseline $(CHART) \
	  --kube-context $(LOCAL_CONTEXT) -n baseline --create-namespace \
	  -f deploy/local/values.yaml
	@echo ""
	@echo "Installed. Watch: kubectl --context $(LOCAL_CONTEXT) -n baseline get pods -w"
	@echo "Baseline:    http://localhost:8080  (LoadBalancer -> localhost; no port-forward)"
	@echo "Dashboard:   http://localhost:8081  (read-only UI; 'view as' header auth)"

.PHONY: ui-dev
ui-dev: ## Run the dashboard with hot-reload (Vite :5173, proxies /api -> VITE_BACKEND_TARGET||:8080)
	cd frontend && pnpm install && pnpm dev

.PHONY: ui-image
ui-image: ## Build the UI image and load it into Docker Desktop's node
	docker build -t baseline-ui:dev frontend
	docker save baseline-ui:dev | docker exec -i $(K8S_NODE) ctr -n k8s.io images import - >/dev/null
	kubectl --context $(LOCAL_CONTEXT) -n baseline rollout restart deployment baseline-baseline-ui 2>/dev/null || true

.PHONY: local-seed
local-seed: ## Seed org namespace + grants on the local cluster (PRINCIPAL=you)
	CONTEXT=$(LOCAL_CONTEXT) ./deploy/seed.sh

.PHONY: local-down
local-down: ## Uninstall the local release (add CLEAN=1 to also delete PVCs)
	helm --kube-context $(LOCAL_CONTEXT) -n baseline uninstall baseline || true
	@[ "$(CLEAN)" = "1" ] && kubectl --context $(LOCAL_CONTEXT) -n baseline delete pvc --all || true

## --- Raspberry Pi cluster ---------------------------------------------

PG_IMAGE := $(PI_REGISTRY)/baseline-postgresql
PG_TAG   ?= 16-pgvector

.PHONY: pi-pg-image
pi-pg-image: ## Build + push the custom Bitnami+pgvector Postgres image (run once / on PG bumps)
	docker buildx build --platform linux/arm64 -t $(PG_IMAGE):$(PG_TAG) --push deploy/postgres

MEM0_IMAGE := $(PI_REGISTRY)/baseline-mem0-api

.PHONY: pi-mem0-image
pi-mem0-image: ## Build + push the mem0-api image (OpenAI default; adds the DB/graph drivers the stock image lacks)
	docker buildx build --platform linux/arm64 -t $(MEM0_IMAGE):openai --push deploy/mem0-api

.PHONY: pi-mem0-image-ollama
pi-mem0-image-ollama: ## Build + push the Ollama-patched variant (self-hosted; needs a GPU node to be usable)
	docker buildx build --platform linux/arm64 --build-arg PATCH_OLLAMA=1 -t $(MEM0_IMAGE):ollama --push deploy/mem0-api

.PHONY: pi-image
pi-image: ## Build the linux/arm64 image for the Pi registry (tag = git SHA)
	docker buildx build --platform linux/arm64 -t $(IMAGE):$(PI_TAG) --load .

.PHONY: pi-push
pi-push: ## Build + push the arm64 image to the in-cluster registry
	docker buildx build --platform linux/arm64 -t $(IMAGE):$(PI_TAG) --push .

.PHONY: pi-deploy
pi-deploy: pi-push helm-deps ## Build + push + helm upgrade on the Pi cluster
	helm upgrade --install baseline $(CHART) \
	  --kube-context $(PI_CONTEXT) -n baseline --create-namespace \
	  -f deploy/pi/values.yaml \
	  $(if $(wildcard deploy/pi/secrets.yaml),-f deploy/pi/secrets.yaml,) \
	  --set-string image.tag=$(PI_TAG)
	kubectl --context $(PI_CONTEXT) -n baseline rollout status deployment/baseline-baseline --timeout=180s

.PHONY: pi-seed
pi-seed: ## Seed a namespace + grants on the cluster (PRINCIPAL=you)
	./deploy/seed.sh
