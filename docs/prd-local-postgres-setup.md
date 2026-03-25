# PRD: Local PostgreSQL Development Setup

## Problem Statement

The project has application code wired up for PostgreSQL (connection wrapper, CLI flags, migration Makefile targets) but no local database instance to connect to. Developers have no way to start a local Postgres, and there's no documentation explaining what tools are needed or how to get the dev environment running. This blocks all future work that depends on a database — schema design, API development, and testing against real data.

## Solution

Provide a turnkey local development database setup using Docker Compose with a PostgreSQL 17 container, managed through simple Makefile targets. Include a README documenting all prerequisites and the getting-started workflow so that any developer can go from clone to running app with a connected database in minutes.

## User Stories

1. As a developer, I want to start a local PostgreSQL instance with a single command, so that I can develop against a real database without manual setup.
2. As a developer, I want to stop the local database with a single command, so that I can free up resources when I'm not working on the project.
3. As a developer, I want my database data to persist across stop/start cycles, so that I don't lose my development data every time I restart the container.
4. As a developer, I want to completely reset my database to a clean state with a single command, so that I can start fresh when my data is corrupted or I want to squash migrations.
5. As a developer, I want a clear error message when I try to run migrations or connect via psql while the database is not running, so that I know to start the container first instead of debugging connection errors.
6. As a developer, I want to create new SQL migration files with a single command, so that I can iterate on the schema quickly during the discovery phase.
7. As a developer, I want to run pending migrations against my local database, so that my schema stays up to date as I pull changes.
8. As a developer, I want to roll back migrations, so that I can undo schema changes during development.
9. As a developer, I want to connect to the local database via psql for ad-hoc queries, so that I can inspect data and debug issues.
10. As a developer, I want a README that lists all prerequisites (Go, Docker, goose), so that I know what to install before starting.
11. As a developer, I want step-by-step getting-started instructions in the README, so that I can go from clone to running app quickly.
12. As a developer, I want a reference of all available Makefile targets in the README, so that I can discover what commands are available without reading the Makefile.
13. As a developer, I want the database credentials hardcoded in the Compose file, so that I don't need to configure anything for local development.
14. As a developer, I want the local setup to be independent from production configuration, so that the production database (e.g., RDS) uses entirely separate credentials and connection details managed by the deployment environment.
15. As a developer, I want to be able to seed the database with test data in the future, so that I can test against realistic data copied from production.

## Implementation Decisions

- **Docker Compose for local Postgres**: Use a `compose.yml` at the project root defining a single PostgreSQL 17 service. This is the standard location and allows adding future services (Redis, queues, etc.) to the same file.
- **Hardcoded dev credentials**: The Compose file will hardcode `POSTGRES_USER=rssapp`, `POSTGRES_PASSWORD=dev-password`, `POSTGRES_DB=rssapp_dev`. The `.env` file will contain a matching DSN (`RSS_DB_DSN`). No shared variable interpolation — these are static local dev values that rarely change. Production will use entirely separate configuration via deployment environment (env vars, Secrets Manager, etc.).
- **Named volume for persistence**: A Docker named volume will store Postgres data across container restarts. Developers can wipe it explicitly with `make db/reset` or `docker compose down -v`.
- **New Makefile targets**:
  - `db/start` — runs `docker compose up -d` to start the Postgres container
  - `db/stop` — runs `docker compose down` to stop the container (data preserved)
  - `db/reset` — runs `docker compose down -v`, then `docker compose up -d`, then `goose up` to provide a completely fresh database with all migrations applied
- **Prerequisite check target**: A `db/check` Makefile helper that verifies the Postgres container is running. It will be wired as a dependency on `db/psql`, `db/migrations/up`, and `db/migrations/down`. If the container is not running, it prints a friendly message directing the developer to run `make db/start`.
- **Existing targets unchanged**: `db/psql`, `db/migrations/up`, `db/migrations/down`, and `db/migrations/new` already point at `./migrations` and use `${RSS_DB_DSN}` from `.env`. No modifications needed.
- **Migrations directory**: An empty `migrations/` directory at the project root. No initial migration — schema design will happen incrementally as APIs and features are built. The expectation is to iterate freely during the discovery phase and consolidate migrations before production.
- **Goose as external CLI**: Goose remains an external tool installed via `go install` or Homebrew, not embedded in the Go application or run via Docker. This keeps migration execution explicit and decoupled from application deployment.
- **Migration format**: SQL only, using goose annotations (`-- +goose Up` / `-- +goose Down`).
- **README at project root**: A new `README.md` covering project overview, prerequisites, getting-started workflow, and a reference of available Makefile targets.

## Testing Decisions

- **No automated tests for this change.** This is entirely infrastructure and configuration: a YAML file, Makefile rules, a directory placeholder, and documentation. There is no application logic to test.
- **Manual validation**: The implementation is validated by running through the full workflow: `make db/start` -> `make db/migrations/up` -> `make db/psql` -> `make db/stop` -> `make db/reset`. Additionally, verify that running `make db/psql` or `make db/migrations/up` with the container stopped produces a friendly error message.
- **Prior art**: Existing tests in the codebase (`cmd/api/application_test.go`, `internal/server/*_test.go`) use the standard Go `testing` package with `httptest`. Future database-related tests (e.g., for repository/model layers) would follow the same patterns.

## Out of Scope

- **Schema design and initial migrations**: Tables, indexes, and relationships will be designed separately as APIs and features are built.
- **Data seeding**: Infrastructure for seeding the local database with test or production-derived data is a future concern. The named volume approach supports this — seeded data persists until explicitly wiped.
- **Containerizing the Go application**: The API itself is not being Dockerized. Only Postgres runs in a container.
- **Production database setup**: RDS configuration, IAM auth, connection pooling, and production migration strategy are separate concerns.
- **CI/CD database integration**: Running migrations or database-dependent tests in CI pipelines is not addressed here.

## Further Notes

- The project is in a discovery/iteration phase. The expectation is that migrations will be freely created and destroyed during development, then consolidated into a clean set before going to production.
- The `compose.yml` is designed to grow — additional services can be added as simple service blocks in the same file without restructuring.
- Consider installing the `gh` CLI (`brew install gh`) to enable creating GitHub issues and PRs directly from the terminal in future workflows.
