# AllOnes — build the dashboard, embed it, produce one binary.
GOTOOLCHAIN ?= local
export GOTOOLCHAIN
BIN := allones
ifeq ($(OS),Windows_NT)
	BIN := allones.exe
endif

# Stamp the binary with the version from the VERSION file.
VERSION := $(shell cat VERSION 2>/dev/null)
LDFLAGS := -s -w -X github.com/allones/allones/internal/version.Version=$(VERSION)

.PHONY: all build build-web build-bin dev dev-web test vet clean run

all: build

build: build-web build-bin ## Full build: dashboard + binary

build-web: ## Build the React dashboard and embed it
	cd web && (bun install && bun run build || (npm install && npm run build))
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -Command "if (Test-Path internal/server/dist) { Remove-Item internal/server/dist -Recurse -Force }; Copy-Item web/dist internal/server/dist -Recurse"
else
	rm -rf internal/server/dist
	cp -r web/dist internal/server/dist
endif

build-bin: ## Build just the Go binary (uses whatever is in internal/server/dist)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/allones

dev: ## Backend (:8788) + Vite dev server (:5273) — run in two terminals
	@echo "Terminal 1: make build-bin && ./$(BIN)"
	@echo "Terminal 2: make dev-web"

dev-web:
	cd web && bun run dev

run: build-bin
	./$(BIN)

test: ## Go tests
	go test ./...

vet:
	go vet ./...

clean:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -Command "Remove-Item $(BIN), web/dist, web/node_modules -Recurse -Force -ErrorAction SilentlyContinue"
else
	rm -f $(BIN)
	rm -rf web/dist web/node_modules
endif
