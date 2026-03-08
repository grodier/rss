include .env

# ==================================================================================== #
# HELPERS
# ==================================================================================== #

## help: Show this help message
.PHONY: help
help:
	@echo "Usage:"
	@sed -n 's/^##//p' Makefile | column -t -s ':' |  sed -e 's/^/ /'

.PHONY: confirm
confirm:
	@echo -n "Are you sure? [y/N] " && read ans && [ $${ans:-N} = y ]

# ==================================================================================== #
# DEVELOPMENT
# ==================================================================================== #
.PHONY: run
run:
	air

.PHONY: build
build:
	go build -o bin/api ./cmd/api

# ==================================================================================== #
# TEST
# ==================================================================================== #
.PHONY: test
test:
	go test -v ./...

.PHONY: test/cover
test/cover:
	go test -coverprofile=/tmp/coverage.out ./...
	go tool cover -html=/tmp/coverage.out
