GO     := GO15VENDOREXPERIMENT=1 go
GOPATH := $(firstword $(subst :, ,$(GOPATH)))
PROMU  ?= $(GOPATH)/bin/promu
pkgs    = $(shell $(GO) list ./... | grep -v /vendor/)

PREFIX                  ?= $(shell pwd)
BIN_DIR                 ?= $(shell pwd)
DOCKER_IMAGE_NAME       ?= ghcr.io/zekker6/sentry_exporter
DOCKER_IMAGE_TAG        ?= $(subst /,-,$(shell git rev-parse --abbrev-ref HEAD))


all: style build test

style:
	@echo ">> checking code style"
	@! gofmt -d $(shell find . -path ./vendor -prune -o -name '*.go' -print) | grep '^'

test:
	@echo ">> running tests"
	@$(GO) test -short $(pkgs)

format:
	@echo ">> formatting code"
	@$(GO) fmt $(pkgs)

vet:
	@echo ">> vetting code"
	@$(GO) vet $(pkgs)

docker:
	@echo ">> building docker image"
	@docker build --file docker/Dockerfile -t "$(DOCKER_IMAGE_NAME):$(DOCKER_IMAGE_TAG)" .

.PHONY: all style format build test vet docker
