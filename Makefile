.PHONY: fmt lint lint-tools coverage hooks tidy server-run

LINT_BIN_DIR := $(shell go env GOCACHE)/go-software-factory-lint
COVERAGE_BASE ?= origin/main

fmt:
	find . -type d \( -name .git -o -name vendor \) -prune -o -type f -name '*.go' -exec gofmt -w {} +

lint lint-tools: export GOPROXY = off
lint lint-tools: export GOSUMDB = off
lint: export GOFLAGS := $(GOFLAGS) -trimpath
lint: fmt lint-tools
	./lint/check-test-files.sh
	"$(LINT_BIN_DIR)/go-arch-lint" check --arch-file lint/architecture-layers.yml
	"$(LINT_BIN_DIR)/go-arch-lint" check --arch-file lint/architecture-services.yml
	./lint/check-architecture.sh
	"$(LINT_BIN_DIR)/golangci-lint" run ./...

lint-tools:
	mkdir -p "$(LINT_BIN_DIR)"
	go -C lint/go-arch-lint build -trimpath -o "$(LINT_BIN_DIR)/go-arch-lint" github.com/fe3dback/go-arch-lint
	go -C lint/golangci-lint build -trimpath -o "$(LINT_BIN_DIR)/golangci-lint" github.com/golangci/golangci-lint/v2/cmd/golangci-lint

coverage: export GOPROXY = off
coverage: export GOSUMDB = off
coverage:
	python3 lint/check_coverage_test.py
	go test -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out > coverage.txt
	cat coverage.txt
	@if [ -n "$$GITHUB_STEP_SUMMARY" ]; then \
		{ echo '### Go code coverage'; echo; echo '```text'; tail -n 1 coverage.txt; echo '```'; } >> "$$GITHUB_STEP_SUMMARY"; \
	fi
	python3 lint/check_coverage.py --base "$(COVERAGE_BASE)"

hooks:
	git config --local core.hooksPath .githooks

tidy:
	go mod tidy

server-run:
	go run ./cmd/server
