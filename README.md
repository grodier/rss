# RSS

An RSS feed API built with Go and PostgreSQL.

## Prerequisites

- [Go](https://go.dev/dl/) 1.25+
- [Docker](https://docs.docker.com/get-docker/) (for local PostgreSQL)
- [Goose](https://github.com/pressly/goose) — database migration tool
  ```bash
  go install github.com/pressly/goose/v3/cmd/goose@latest
  ```
- [Air](https://github.com/air-verse/air) (optional) — hot reload for development
  ```bash
  go install github.com/air-verse/air@latest
  ```

## Getting Started

1. Clone the repository and copy the example environment file:

   ```bash
   cp .env.example .env
   ```

2. Start the database:

   ```bash
   make db/start
   ```

3. Run migrations:

   ```bash
   make db/migrations/up
   ```

4. Start the development server:

   ```bash
   make run
   ```

   The API is available at `http://localhost:8080`.

## Makefile Targets

Run `make help` to see all targets. Summary:

### Development

| Target | Description |
|---|---|
| `make run` | Start the dev server with hot reload (air) |
| `make build` | Build the binary to `bin/api` |

### Testing

| Target | Description |
|---|---|
| `make test` | Run all tests |
| `make test/cover` | Generate and view test coverage report |

### Database

| Target | Description |
|---|---|
| `make db/start` | Start the PostgreSQL container |
| `make db/stop` | Stop the PostgreSQL container (data is preserved) |
| `make db/reset` | Destroy the database, start fresh, and re-run all migrations |
| `make db/psql` | Connect to the database using psql |
| `make db/migrations/new name=<name>` | Create a new SQL migration file |
| `make db/migrations/up` | Apply all pending migrations |
| `make db/migrations/down` | Roll back the last migration |
