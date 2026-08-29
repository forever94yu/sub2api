.PHONY: wire build build-embed test-unit test-integration test-e2e test-cover-integration clean-coverage clean

PROJECT_ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/..)
BACKEND_DIR := $(PROJECT_ROOT)/backend

VERSION ?= $(shell cd "$(BACKEND_DIR)" && ./scripts/resolve-version.sh)
LDFLAGS ?= -s -w -X main.Version=$(VERSION)

wire:
	@echo "生成 Wire 代码..."
	@$(MAKE) -C "$(BACKEND_DIR)" generate
	@echo "Wire 代码生成完成"

build:
	@echo "构建后端（不嵌入前端）..."
	@cd "$(BACKEND_DIR)" && CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -trimpath -o bin/server ./cmd/server
	@echo "构建完成: backend/bin/server"

build-embed:
	@echo "构建后端（嵌入前端）..."
	@cd "$(BACKEND_DIR)" && CGO_ENABLED=0 go build -tags embed -ldflags="$(LDFLAGS)" -trimpath -o bin/server ./cmd/server
	@echo "构建完成: backend/bin/server (with embedded frontend)"

test-unit:
	@cd "$(BACKEND_DIR)" && go test -tags unit ./... -count=1

test-integration:
	@cd "$(BACKEND_DIR)" && go test -tags integration ./... -count=1 -race -parallel=8

test-e2e:
	@echo "运行 E2E 测试（需要本地服务器运行）..."
	@cd "$(BACKEND_DIR)" && go test -tags e2e ./internal/integration/... -count=1 -v -timeout=300s

test-cover-integration:
	@echo "运行集成测试并生成覆盖率报告..."
	@cd "$(BACKEND_DIR)" && go test -tags=integration -cover -coverprofile=coverage.out -count=1 -race -parallel=8 ./...
	@cd "$(BACKEND_DIR)" && go tool cover -func=coverage.out | tail -1
	@cd "$(BACKEND_DIR)" && go tool cover -html=coverage.out -o coverage.html
	@echo "覆盖率报告已生成: backend/coverage.html"

clean-coverage:
	@rm -f "$(BACKEND_DIR)/coverage.out" "$(BACKEND_DIR)/coverage.html"
	@echo "覆盖率文件已清理"

clean: clean-coverage
	@rm -rf "$(BACKEND_DIR)/bin/"
	@echo "构建产物已清理"
