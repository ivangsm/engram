.DEFAULT_GOAL := help

BIN_DIR ?= bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "v0.1.0-dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
LDFLAGS = -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

DOCKER_IMAGE ?= ghcr.io/ivangsm/engram:latest

.PHONY: help
help: ## Mostrar comandos disponibles
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Compilar binarios de engram (CLI) y engramd (daemon)
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/engram ./cmd/engram
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/engramd ./cmd/engramd

.PHONY: test
test: ## Ejecutar tests unitarios y de integración con coverage
	go test -v -race -coverprofile=coverage.txt -covermode=atomic ./...

.PHONY: lint
lint: ## Ejecutar análisis estático con golangci-lint
	golangci-lint run ./...

.PHONY: verify
verify: lint test build ## Ejecutar suite completa de verificación (lint, test, build)

.PHONY: docker-build
docker-build: ## Construir imagen optimizada de Docker localmente
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t $(DOCKER_IMAGE) .

.PHONY: docker-refresh
docker-refresh: build docker-build ## Reconstruir CLI y recrear contenedor engramd con la imagen local
	docker compose up -d --no-deps engramd

.PHONY: install-hooks
install-hooks: ## Activar Git hooks locales (.githooks/pre-push)
	chmod +x .githooks/*
	git config core.hooksPath .githooks
	@echo "Git hooks activados (core.hooksPath=.githooks)"

.PHONY: release
release: ## Etiquetar, publicar release y reconstruir stack local (uso: make release VERSION=v0.3.1)
	@test -n "$(VERSION)" || (echo "Error: especifica VERSION=vX.Y.Z" >&2; exit 1)
	@echo "$(VERSION)" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+' || (echo "Error: VERSION debe tener formato semver vX.Y.Z" >&2; exit 1)
	$(MAKE) verify
	git tag -a "$(VERSION)" -m "Release $(VERSION)"
	git push origin main "$(VERSION)"
	@if command -v gh >/dev/null 2>&1; then \
		gh workflow run ci.yml --ref main; \
		gh workflow run docker-publish.yml --ref "$(VERSION)"; \
		gh workflow run release.yml --ref "$(VERSION)"; \
	fi

.PHONY: compose-up
compose-up: ## Iniciar postgres y daemon engramd en segundo plano
	docker compose up -d

.PHONY: compose-down
compose-down: ## Detener y remover servicios de compose
	docker compose down

.PHONY: compose-logs
compose-logs: ## Inspeccionar logs de docker compose
	docker compose logs -f

.PHONY: clean
clean: ## Limpiar binarios y artefactos temporales
	rm -rf $(BIN_DIR) coverage.txt
