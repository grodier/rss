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

.PHONY: test/integration
test/integration: db/check
	go test -v -tags integration ./...

.PHONY: test/cover
test/cover:
	go test -coverprofile=/tmp/coverage.out ./...
	go tool cover -html=/tmp/coverage.out

# ==================================================================================== #
# DATABASE
# ==================================================================================== #

## db/start: start the database container
.PHONY: db/start
db/start:
	docker compose up -d

## db/stop: stop the database container
.PHONY: db/stop
db/stop:
	docker compose down

## db/reset: destroy the database and rebuild from scratch
.PHONY: db/reset
db/reset: confirm
	docker compose down -v
	docker compose up -d
	@echo "Waiting for database to be ready..."
	@until docker compose exec postgres pg_isready -U rssapp > /dev/null 2>&1; do sleep 0.5; done
	@echo "Running up migrations..."
	goose -dir ./migrations postgres ${RSS_DB_DSN} up

.PHONY: db/check
db/check:
	@docker compose ps --status running | grep -q postgres || (echo "Error: Database is not running. Run 'make db/start' first." && exit 1)

## db/psql: connect to the database using psql
.PHONY: db/psql
db/psql: db/check
	psql ${RSS_DB_DSN}

## db/migrations/new name=$1: create a new database migration
.PHONY: db/migrations/new
db/migrations/new:
	@echo "Creating migration files for ${name}..."
	goose -s -dir ./migrations create ${name} sql

## db/migrations/up: apply all up database migrations
.PHONY: db/migrations/up
db/migrations/up: db/check confirm
	@echo "Running up migrations..."
	goose -dir ./migrations postgres ${RSS_DB_DSN} up

## db/migrations/down: apply all down database migrations
.PHONY: db/migrations/down
db/migrations/down: db/check confirm
	@echo "Running down migrations..."
	goose -dir ./migrations postgres ${RSS_DB_DSN} down
