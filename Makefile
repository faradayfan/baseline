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
