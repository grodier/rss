# Plan: Local PostgreSQL Development Setup

> Source PRD: `docs/prd-local-postgres-setup.md`

## Architectural decisions

Durable decisions that apply across all phases:

- **Compose**: `compose.yml` at project root, Postgres 17 image, single service for now (additional services added to same file as needed)
- **Credentials**: Hardcoded in Compose — user `rssapp`, password `dev-password`, database `rssapp_dev`, port `5432`. No variable interpolation. Production uses entirely separate configuration.
- **DSN**: `RSS_DB_DSN` env var in `.env` with matching connection string. Consumed by Makefile for goose and psql.
- **Persistence**: Docker named volume for Postgres data. Survives `docker compose down`. Wiped explicitly with `down -v`.
- **Migrations**: Goose as external CLI tool, SQL format only, `migrations/` directory at project root. No initial migration — schema design happens separately.
- **Makefile convention**: All database targets under `db/*` namespace. New targets follow existing patterns.

---

## Phase 1: Container lifecycle — start and stop Postgres

**User stories**: 1, 2, 3, 13, 14

### What to build

A Docker Compose configuration that runs Postgres 17 locally with hardcoded dev credentials and a named volume for data persistence. Two new Makefile targets (`db/start` and `db/stop`) manage the container lifecycle. An empty `migrations/` directory is created as a placeholder for future schema work.

After this phase, a developer can start Postgres with `make db/start`, verify it works with `make db/psql`, and stop it with `make db/stop`. Data persists across stop/start cycles.

### Acceptance criteria

- [ ] `compose.yml` exists at project root with a Postgres 17 service, hardcoded credentials matching the `.env` DSN, and a named volume
- [ ] `make db/start` starts the Postgres container in the background
- [ ] `make db/stop` stops the container without destroying the volume
- [ ] `make db/psql` connects successfully to the running container
- [ ] Data persists after `make db/stop` followed by `make db/start`
- [ ] `migrations/` directory exists at project root (empty, with `.gitkeep` or equivalent)

---

## Phase 2: Safety and reset workflow

**User stories**: 4, 5

### What to build

A Makefile prerequisite check (`db/check`) that verifies the Postgres container is running before allowing connection-dependent targets to proceed. If the container is not running, it prints a friendly message directing the developer to `make db/start` and aborts. This check is wired as a dependency on `db/psql`, `db/migrations/up`, and `db/migrations/down`.

A `db/reset` target that performs a full teardown (destroys the volume), brings up a fresh container, and runs all migrations — providing a one-command path to a clean database.

### Acceptance criteria

- [ ] Running `make db/psql` with the container stopped prints a friendly error message mentioning `make db/start` instead of a raw connection error
- [ ] Running `make db/migrations/up` with the container stopped prints the same friendly error
- [ ] Running `make db/migrations/down` with the container stopped prints the same friendly error
- [ ] `make db/migrations/new` works regardless of container state (it only creates files)
- [ ] `make db/reset` destroys the volume, starts a fresh container, and runs all pending migrations
- [ ] After `make db/reset`, the database is in a clean state with the current schema applied

---

## Phase 3: Developer documentation

**User stories**: 10, 11, 12

### What to build

A `README.md` at the project root that serves as the entry point for any developer setting up the project. It covers prerequisites (Go, Docker, goose), a step-by-step getting-started workflow (clone, start db, run migrations, start app), and a reference table of all available Makefile targets with descriptions.

### Acceptance criteria

- [ ] `README.md` exists at project root
- [ ] Prerequisites section lists Go, Docker, and goose with install guidance
- [ ] Getting-started section walks through the full workflow: `make db/start` -> `make db/migrations/up` -> `make run`
- [ ] Makefile reference section documents all available targets (development, testing, and database)
- [ ] A new developer can follow the README from clone to running app without additional guidance
