MODULE := github.com/Alexander-D-Karpov/calendar
BIN := bin/calendar
BUILDINFO := $(MODULE)/internal/buildinfo
VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --verify --quiet HEAD 2>/dev/null)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X $(BUILDINFO).Version=$(VERSION) -X $(BUILDINFO).Commit=$(COMMIT) -X $(BUILDINFO).Date=$(DATE)
SWAGGER_DIR := web/static/vendor/swagger-ui
SWAGGER_UI_VERSION ?= $(shell cat $(SWAGGER_DIR)/VERSION 2>/dev/null || echo latest)

.DEFAULT_GOAL := help
.PHONY: help build run test test-integration vet sqlc css tidy vendor-swagger vendor-swagger-update docker clean

help: ## Show this help
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "} {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

build: ## Build bin/calendar with version, commit and date
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/calendar

run: ## Run the server with build info
	go run -ldflags "$(LDFLAGS)" ./cmd/calendar serve

test: ## Run unit tests
	go test -count=1 ./...

test-integration: ## Run unit and integration tests against TEST_DATABASE_URL
	go test -tags=integration -count=1 ./...

vet: ## Vet with and without the integration tag
	go vet ./...
	go vet -tags=integration ./...

sqlc: ## Generate database code
	sqlc generate

css: ## Regenerate web/static/css/slots.css
	go run ./internal/tools/gengrid

tidy: ## Tidy go.mod
	go mod tidy

vendor-swagger: ## Vendor Swagger UI at the pinned version
	go run ./internal/tools/vendorswagger -version $(SWAGGER_UI_VERSION) -out $(SWAGGER_DIR)

vendor-font: ## Vendor the Inter fonts used by the OG image renderer
	go run ./internal/tools/vendorfont

vendor-swagger-update: ## Vendor the latest Swagger UI release
	go run ./internal/tools/vendorswagger -version latest -out $(SWAGGER_DIR)

docker: ## Build the container image
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t calendar:$(VERSION) .

clean: ## Remove build output
	rm -rf bin