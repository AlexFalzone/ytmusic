.PHONY: help build build-web up down logs clean test

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X ytmusic/internal/buildinfo.Version=$(VERSION)

help:
	@echo "ytmusic - Makefile commands"
	@echo ""
	@echo "Docker commands:"
	@echo "  make build      - Build CLI Docker image"
	@echo "  make build-web  - Build web Docker image"
	@echo "  make up         - Start web service"
	@echo "  make down       - Stop all services"
	@echo "  make logs       - Show web service logs"
	@echo "  make clean      - Remove all containers and images"
	@echo ""
	@echo "Local build:"
	@echo "  make local      - Build local binaries"
	@echo "  make test       - Run tests"

build:
	docker compose build --build-arg VERSION=$(VERSION) ytmusic-cli

build-web:
	docker compose build --build-arg VERSION=$(VERSION) ytmusic-web

up:
	docker compose up -d ytmusic-web

down:
	docker compose down

logs:
	docker compose logs -f ytmusic-web

clean:
	docker compose down -v --rmi all

local:
	go build -ldflags "$(LDFLAGS)" -o ytmusic ./cmd/ytmusic
	go build -ldflags "$(LDFLAGS)" -o ytmusic-web ./cmd/ytmusic-web

test:
	go test -race ./... && go vet ./...