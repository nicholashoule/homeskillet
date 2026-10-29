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
PLATFORMS := linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64 windows-arm64

# The race detector needs cgo on Linux, and Go disables cgo when no C compiler
# is installed, as on a fresh WSL Ubuntu or a slim container image. Enable
# -race only when cgo is on. Override with `make test RACE=` (for example on
# musl-based images such as Alpine, where the race detector is unsupported) or
# `make test RACE=-race`. `?=` defers the `go env` call until RACE is used.
RACE ?= $(if $(filter 1,$(shell go env CGO_ENABLED)),-race)

# The executable suffix of this machine's builds: .exe on Windows, which runs
# no file without one, and empty elsewhere. The $(eval) asks go once, when a
# recipe first needs it, and a plain = keeps an environment variable named EXE
# from renaming the build.
EXE = $(eval EXE := $(shell go env GOEXE))$(EXE)

# Sibling configuration repository used by `make integration`.
INTEGRATION_REPO ?= ../dotfiles-linux

# Version stamping. Building in a Git checkout with Go 1.24 or newer records
# the tag (v0.0.1), or a pseudo-version for an untagged commit, plus the commit
# and whether the tree was modified; `homeskillet version` prints them. Set
# VERSION only where no Git metadata exists, such as a container build that
# excludes .git: `make install VERSION=v0.0.1`. In a checkout, tag instead: a
# stamped build differs from the tagged one, and installed versions are never
# rewritten. VERSION must be a semantic version with a leading v. Plain
# assignments, not ?=: an environment variable named VERSION or PREFIX, as a
# container image may set, must not stamp or relocate a build; only the
# command line (make build VERSION=v0.0.1) overrides these.
VERSION :=

# Build time, which Go does not record, for `homeskillet version`. It is the
# current time, or SOURCE_DATE_EPOCH for a reproducible build; BUILT= leaves
# it out. Make 3.81 has no clock, so tools/buildtime reads it. The $(eval)
# runs that once, when a build first needs it, not on every make invocation.
BUILT ?= $(eval BUILT := $(shell go run ./tools/buildtime))$(BUILT)

# Linker flags for every build. -s and -w drop the symbol table and DWARF debug
# data, about a third of the binary. Neither is needed at run time: panics still
# print function names and lines, and the build details `homeskillet version`
# reports live in a section of their own.
STAMP = github.com/nicholashoule/homeskillet/internal/version
LDFLAGS = -s -w$(if $(BUILT), -X $(STAMP).built=$(BUILT))$(if $(VERSION), -X $(STAMP).stamped=$(VERSION))

# Installation prefix for `make install`; empty means ~/.local. The binary goes
# to $(PREFIX)/share/homeskillet/versions/<version>/ and the command to
# $(PREFIX)/bin/homeskillet.
PREFIX :=

.DEFAULT_GOAL := help

##@ General

help: ## Show this help
	@$(XPLAT) help $(MAKEFILE_LIST)

##@ Development

fmt: ## Format sources (gofmt -s and goimports, via golangci-lint)
	golangci-lint fmt ./...

# Vetting for macOS and Windows as well catches code that breaks those builds
# from any machine, since each has files of its own.
vet: ## Run go vet for this machine, macOS, and Windows
	go vet ./...
	env GOOS=darwin go vet ./...
	env GOOS=windows go vet ./...

lint: ## Run golangci-lint with .golangci.yml
	golangci-lint run ./...

test: ## Run tests, with the race detector when cgo is available
	go test $(RACE) ./...

# tools/smoke builds homeskillet and uses it as a person would, in a scratch
# home, as CI does on Linux, macOS, and Windows; see docs/development.md.
smoke: ## Build homeskillet, then link and unlink this system's example in a scratch home
	go run ./tools/smoke

site: ## Check the website in site/: its page, and every file it references
	go run ./tools/site

check: vet lint test smoke site ## Run vet, lint, tests, the smoke test, and the site check; required before handing off work

# tools/githooks resolves the hooks directory through Git and never replaces a
# hook it did not install. Run it again after changing tools/githooks/pre-commit.
hooks: ## Install the pre-commit hook: check, repogov, and demojify on each commit
	go run ./tools/githooks

##@ Build

# CGO_ENABLED=0 makes the binary static, so it runs on any distribution or
# container image of the same architecture, as the cross builds do.
build: ## Build a static, stripped bin/homeskillet (VERSION=v0.0.1 to stamp one)
	env CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/$(BINARY)$(EXE) .

install: build ## Install the build as a new version and update latest/previous (PREFIX=~/.local)
	$(BIN)/$(BINARY)$(EXE) self install $(if $(PREFIX),--prefix "$(PREFIX)")

cross: $(addprefix cross-,$(PLATFORMS)) ## Build bin/homeskillet-<os>-<arch> for every release platform

# cross-<os>-<arch> builds one platform. `env` scopes GOOS and GOARCH to this
# one go command, so cross targets never change how xplat or other targets
# build. CGO_ENABLED=0 gives static binaries, -trimpath keeps local filesystem
# paths out of them, and LDFLAGS strips them (see above). Windows runs only
# files with an extension such as .exe, so its builds get one.
cross-%:
	env GOOS=$(word 1,$(subst -, ,$*)) GOARCH=$(word 2,$(subst -, ,$*)) CGO_ENABLED=0 \
		go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/$(BINARY)-$*$(if $(findstring windows,$*),.exe) .

integration: build ## Link the dotfiles-linux manifest into a temporary home (needs bash)
	@$(XPLAT) require $(INTEGRATION_REPO)/tests/check.sh "integration: $(INTEGRATION_REPO)/tests/check.sh not found; set INTEGRATION_REPO=<path>"
	bash $(INTEGRATION_REPO)/tests/check.sh "$(CURDIR)/$(BIN)/$(BINARY)$(EXE)"

clean: ## Remove bin/
	$(XPLAT) rmrf $(BIN)

.PHONY: help fmt vet lint test smoke site check hooks build install cross integration clean
