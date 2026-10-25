# homeskillet Makefile.
#
# Supported hosts: Linux (any distribution, including Docker containers and
# WSL Ubuntu) and macOS. Make runs every recipe with /bin/sh, whatever the
# login shell is, so bash and zsh users get identical behavior.
#
# Portability rules for this file:
#   - macOS ships GNU Make 3.81 (2006). Use nothing newer: no .ONESHELL,
#     no `private` or `export` on target-specific variables, no $(file ...).
#   - Recipes call only go, golangci-lint, env, tools/xplat, and the
#     homeskillet binary they just built. File operations go through xplat
#     rather than mkdir -p, rm -rf, cp, or test, because those flags differ
#     between GNU, BSD, and BusyBox. Symlinks are made by homeskillet itself
#     (see `install`), never by ln.
#   - The one exception is `integration`, which runs a bash script by design.

BINARY    := homeskillet
BIN       := bin
XPLAT     := go run ./tools/xplat
PLATFORMS := linux-amd64 linux-arm64 darwin-amd64 darwin-arm64

# The race detector needs cgo on Linux, and Go disables cgo when no C compiler
# is installed, as on a fresh WSL Ubuntu or a slim container image. Enable
# -race only when cgo is on. Override with `make test RACE=` (for example on
# musl-based images such as Alpine, where the race detector is unsupported) or
# `make test RACE=-race`. `?=` defers the `go env` call until RACE is used.
RACE ?= $(if $(filter 1,$(shell go env CGO_ENABLED)),-race)

# Sibling configuration repository used by `make integration`.
INTEGRATION_REPO ?= ../dotfiles-linux

# Version stamping. Building in a Git checkout with Go 1.24 or newer records
# the tag (v0.0.1), or a pseudo-version for an untagged commit, plus the commit,
# its time, and whether the tree was modified; `homeskillet version` prints
# them. Set VERSION only where no Git metadata exists, such as a container build
# that excludes .git: `make install VERSION=v0.0.1`. In a checkout, tag instead:
# a stamped build differs from the tagged one, and installed versions are never
# rewritten. It must be a semantic version with a leading v. `=` (not `:=`) lets a command-line VERSION take effect.
VERSION ?=
LDFLAGS = $(if $(VERSION),-X github.com/nicholashoule/homeskillet/internal/version.stamped=$(VERSION))

# Installation prefix for `make install`; empty means ~/.local. The binary goes
# to $(PREFIX)/share/homeskillet/versions/<version>/ and the command to
# $(PREFIX)/bin/homeskillet.
PREFIX ?=

.DEFAULT_GOAL := help

##@ General

help: ## Show this help
	@$(XPLAT) help $(MAKEFILE_LIST)

##@ Development

fmt: ## Format sources (gofmt -s and goimports, via golangci-lint)
	golangci-lint fmt ./...

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint with .golangci.yml
	golangci-lint run ./...

test: ## Run tests, with the race detector when cgo is available
	go test $(RACE) ./...

check: vet lint test ## Run vet, lint, and tests; required before handing off work

##@ Build

build: ## Build bin/homeskillet for this machine (VERSION=v0.0.1 to stamp one)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/$(BINARY) .

install: build ## Install the build as a new version and update latest/previous (PREFIX=~/.local)
	$(BIN)/$(BINARY) self install $(if $(PREFIX),--prefix "$(PREFIX)")

cross: $(addprefix cross-,$(PLATFORMS)) ## Build bin/homeskillet-<os>-<arch> for every release platform

# cross-<os>-<arch> builds one platform. `env` scopes GOOS and GOARCH to this
# one go command, so cross targets never change how xplat or other targets
# build. CGO_ENABLED=0 gives static, reproducible binaries; -trimpath keeps
# local filesystem paths out of them.
cross-%:
	env GOOS=$(word 1,$(subst -, ,$*)) GOARCH=$(word 2,$(subst -, ,$*)) CGO_ENABLED=0 \
		go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/$(BINARY)-$* .

integration: build ## Link the dotfiles-linux manifest into a temporary home (needs bash)
	@$(XPLAT) require $(INTEGRATION_REPO)/tests/check.sh "integration: $(INTEGRATION_REPO)/tests/check.sh not found; set INTEGRATION_REPO=<path>"
	bash $(INTEGRATION_REPO)/tests/check.sh "$(CURDIR)/$(BIN)/$(BINARY)"

clean: ## Remove bin/
	$(XPLAT) rmrf $(BIN)

.PHONY: help fmt vet lint test check build install cross integration clean
