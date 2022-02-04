GIT_COMMIT = $(shell git rev-parse HEAD)
GIT_TAG = $(shell git describe --tags 2>/dev/null || echo "v0.0.1" )

PKG        := ./...
LDFLAGS    := -w -s
LDFLAGS += -X "github.com/rancher-sandbox/elemental/internal/version.version=${GIT_TAG}"
LDFLAGS += -X "github.com/rancher-sandbox/elemental/internal/version.gitCommit=${GIT_COMMIT}"


GINKGO?=$(shell which ginkgo 2> /dev/null)
ifeq ("$(GINKGO)","")
GINKGO="/usr/bin/ginkgo"
endif

$(GINKGO):
	@echo "'ginkgo' not found."
	@exit 1

build:
	go build -ldflags '$(LDFLAGS)' -o bin/

vet:
	go vet ${PKG}

fmt:
	go fmt ${PKG}

test_deps:
	go install github.com/onsi/ginkgo/v2/ginkgo

test: $(GINKGO)
	ginkgo run --label-filter !root --fail-fast --slow-spec-threshold 30s --race --covermode=atomic --coverprofile=coverage.txt -p -r ${PKG}

test_root: $(GINKGO)
ifneq ($(shell id -u), 0)
	@echo "This tests require root/sudo to run."
	@exit 1
else
	ginkgo run --label-filter root --fail-fast --slow-spec-threshold 30s --race --covermode=atomic --coverprofile=coverage_root.txt -p -r ${PKG}
endif

license-check:
	@.github/license_check.sh

lint: fmt vet

all: lint test build