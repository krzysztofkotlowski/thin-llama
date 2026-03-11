GO ?= go
BIN ?= thin-llama
CONFIG ?= ./config.example.json

.PHONY: fmt test build run validate-config models pull docker-build docker-run

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./...

build:
	$(GO) build -o ./bin/$(BIN) ./cmd/thin-llama

run:
	$(GO) run ./cmd/thin-llama serve --config $(CONFIG)

validate-config:
	$(GO) run ./cmd/thin-llama validate-config --config $(CONFIG)

models:
	$(GO) run ./cmd/thin-llama models --config $(CONFIG)

pull:
	@echo "Usage: make pull MODEL=<configured-name>"
	@test -n "$(MODEL)"
	$(GO) run ./cmd/thin-llama pull --config $(CONFIG) --model "$(MODEL)"

docker-build:
	docker build -t $(BIN):latest .

docker-run:
	docker compose up --build
