# Find Me Backend

<!-- markdownlint-disable MD033 -->
<p align="center">
  <img src="https://go.dev/images/gophers/ladder.svg" alt="Go Gopher" width="200"/>
</p>
<!-- markdownlint-enable MD033 -->

Find Me API: Chi, pgx, JWT auth, Swagger, Prometheus metrics, and PostgreSQL. Based on the go-chi-postgres starter.

## Features

![CI](https://github.com/AbhishekCS3459/find-me-backend/workflows/CI/badge.svg)
![Go Version](https://img.shields.io/badge/go-1.25+-blue.svg)
![License](https://img.shields.io/badge/license-MIT-green.svg)

> Module path: `github.com/AbhishekCS3459/find-me-backend`

- Chi router
- pgx/v5 PostgreSQL driver and pool
- golang-migrate migrations
- JWT authentication
- Role-aware authorization (`user` / `admin`)
- Password reset and password change flows
- Zerolog structured logging
- Prometheus metrics at `/metrics`
- Swagger UI at `/swagger/index.html`
- Dockerfile and `docker-compose.yml`
- GitHub Actions CI for test, lint, and build
- Optional Redis-backed queue components included in the repo

## Current repo status

This repository is a starter template, but the checked-in app is also a functioning example API.

### What is wired into the running API today

- `POST /api/auth/register`
- `POST /api/auth/login`
- `POST /api/auth/request-password-reset`
- `POST /api/auth/reset-password`
- `POST /api/auth/change-password` (requires JWT)
- `GET /api/users/me` (requires JWT)
- `GET /api/users/{id}` (owner or admin)
- `PUT /api/users/{id}` (owner or admin)
- `GET /api/users` (admin only)
- `POST /api/users` (admin only)
- `DELETE /api/users/{id}` (admin only)
- `PUT /api/users/{id}/role` (admin only)
- `GET /api/health`
- `GET /metrics`
- Swagger UI at `/swagger/index.html`

### What is present in the repo but not fully integrated into the main app flow

- Queue abstractions and workers under `internal/platform/queue/`
- Queue admin handler in `internal/platform/queue/handler.go`
- Queue helper CLIs such as `cmd/test-queue` and `cmd/queue-monitor`

The queue packages are real and testable, but the main API server in `cmd/api/main.go` / `cmd/api/routes.go` does **not** currently initialize a queue or mount queue admin routes.

## Version and platform notes

- Go version: **1.25+**
- Local development docs in this repo recommend **PostgreSQL 18**
- Current Docker Compose and GitHub Actions CI use **PostgreSQL 16** images

So the practical compatibility story right now is: local Postgres 18 is recommended, while the checked-in container/CI baseline is Postgres 16.

## Project structure

```text
cmd/
  api/                 # Process entry: main.go, config.go, routes.go, utils/
  queue-monitor/       # Queue inspection CLI
  test-queue/          # Queue demo/test CLI
internal/
  identity/            # users, retailers, retailer_kyc (auth + user API)
  platform/
    database/          # pgx pool
    middleware/        # JWT, CORS, CSP, rate limit
    queue/             # optional background jobs (not mounted)
    httputil/          # JSON helpers, validation
    health/            # GET /api/health
docs/                  # Swagger output and supporting guides
migrations/            # SQL migrations
tests/                 # Integration tests
```

## Prerequisites

Before you begin, ensure you have:

- **Go 1.25+**
- **PostgreSQL** (18 recommended for local dev; 16 is what Docker/CI currently run)
- **Git**
- **Docker** (optional, for containerized dev and helper services)

Verify your installation:

```bash
go version
psql --version
```

## Quick start

### 1) Clone and install deps

```bash
git clone https://github.com/AbhishekCS3459/find-me-backend.git
cd find-me-backend
go mod tidy
```

### 2) Create your env file

```bash
cp .env.example .env
```

Edit `.env` as needed. Minimum local settings:

```env
DATABASE_URL=postgresql://postgres:postgres@localhost:5434/find_me?sslmode=disable
JWT_SECRET=dev-secret-change-in-production
PORT=8080
ENVIRONMENT=development
LOG_LEVEL=info
BOOTSTRAP_ADMIN_EMAIL=admin@findme.local
BOOTSTRAP_ADMIN_PASSWORD=changeme123
BOOTSTRAP_ADMIN_NAME=Admin
```

Generate a real JWT secret for non-throwaway use:

```bash
openssl rand -base64 32
```

### 3) Create the database

```bash
createdb find_me
```

Alternative:

```bash
psql -d postgres -c 'CREATE DATABASE find_me;'
```

### 4) Install migrate and run migrations

```bash
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
export DATABASE_URL="postgresql://postgres:postgres@localhost:5434/find_me?sslmode=disable"
make migrate-up
```

### 5) Start the API

```bash
make run
```

API docs will be available at:

- API: <http://localhost:8080>
- Health: <http://localhost:8080/api/health>
- Swagger UI: <http://localhost:8080/swagger/index.html>
- Metrics: <http://localhost:8080/metrics>

## Docker development

`docker-compose.yml` starts:

- Postgres on host port `5434`
- Redis on host port `6379`
- API on host port `8080`

Start everything:

```bash
docker compose up -d --build
```

When using Docker for the database from your host shell, use:

```bash
export DATABASE_URL="postgresql://postgres:postgres@localhost:5434/find_me?sslmode=disable"
```

If you only want the database container for local app development:

```bash
make dev
```

That target starts the Compose Postgres service and then runs the API locally with live reload.

## Authentication and authorization

### JWT auth

Primary auth is JWT-based:

1. Register with `POST /api/auth/register`
2. Log in with `POST /api/auth/login`
3. Send `Authorization: Bearer <token>`

### API access token

If `API_ACCESS_TOKEN` is set, requests may also authenticate with:

```text
X-API-Token: <token>
```

This is useful for service-to-service access to routes protected only by the JWT middleware.

**Important:** admin-only routes still require admin role context. The API access token bypass does **not** establish a user role, so it does not grant admin access to endpoints like `GET /api/users`.

### Authorization model

- Public:
  - `GET /api/health`
  - `POST /api/auth/register`
  - `POST /api/auth/login`
  - `POST /api/auth/request-password-reset`
  - `POST /api/auth/reset-password`
- Authenticated user:
  - `POST /api/auth/change-password`
  - `GET /api/users/me`
- Owner or admin:
  - `GET /api/users/{id}`
  - `PUT /api/users/{id}`
- Admin only:
  - `GET /api/users`
  - `POST /api/users`
  - `DELETE /api/users/{id}`
  - `PUT /api/users/{id}/role`

## Example API flow

Register:

```bash
curl -X POST http://localhost:8080/api/auth/register \
  -H "Content-Type: application/json" \
  -d '{"email":"test@example.com","phone":"+15550001000","password":"password123"}'
```

Login:

```bash
curl -X POST http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"test@example.com","password":"password123"}'
```

Get your own profile with the returned token:

```bash
curl -X GET http://localhost:8080/api/users/me \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"
```

## Make targets

Common targets:

```bash
make run
make run-dev
make dev
make stop
make fmt
make vet
make lint
make test
make test-coverage
make migrate-up
make migrate-down
make migrate-status
make swagger
make docker-up
make docker-down
```

There is also a mirrored `justfile`, so you can use `just run`, `just test`, etc.

## Testing

Repo tests currently live primarily in `tests/api_test.go`.

Run them with:

```bash
make test
```

For coverage:

```bash
make test-coverage
```

CI currently runs:

- tests with PostgreSQL 16 service container
- golangci-lint
- binary build

## Migrations

```bash
make migrate-create NAME=add_users_table
make migrate-up
make migrate-down
make migrate-status
```

## Swagger docs

Generate Swagger artifacts:

```bash
make swagger
```

Then open:

<http://localhost:8080/swagger/index.html>

## Documentation

- [Quick Start Guide](./docs/QUICKSTART.md)
- [Setup Guide](./docs/SETUP.md)
- [Authentication Guide](./docs/AUTHENTICATION.md)
- [Testing Guide](./docs/TESTING.md)
- [Deployment Guide](./docs/DEPLOYMENT.md)
- [Postman Setup Guide](./docs/POSTMAN_SETUP.md)
- [Postman Get Users Guide](./docs/POSTMAN_GET_USERS.md)
- [Goroutines Guide](./docs/GOROUTINES.md)
- [Queue Usage Guide](./docs/QUEUE_USAGE.md)
- [Contributing](./CONTRIBUTING.md)

## Maintainer

**Abhishek Kumar Vema**

- GitHub: [@AbhishekCS3459](https://github.com/AbhishekCS3459)

Original starter: [justyn-clark/go-chi-postgres-starter](https://github.com/justyn-clark/go-chi-postgres-starter).

## License

MIT — see `LICENSE`.

## Domain-driven package layout

The API is organized by feature under `internal/`, not by layer under `cmd/api/`. `cmd/api` only starts the process, loads config, and mounts each domain’s `Routes()`.

```text
internal/
  identity/          → users, retailers, retailer_kyc
  platform/
    database/        → pgx pool (moved from cmd/api/database/)
    middleware/      → JWT, CORS, CSP, rate limit (moved from cmd/api/middleware/)
    queue/           → optional jobs (moved from cmd/api/queue/; not mounted)
    httputil/        → JSON write/decode, validation, error body
    health/          → GET /api/health

cmd/api/
  main.go            → connect DB, bootstrap admin, listen
  config.go          → env config
  routes.go          → chi middleware stack + mount domain routers
  utils/             → goroutines, pagination (shared helpers, not a domain)
  errors/            → unused APIError helpers (legacy)
```

Each domain folder uses the same five files:

| File | Role |
|---|---|
| `model.go` | Structs matching this domain’s tables + request/response DTOs |
| `repository.go` | `Repository` interface + unexported impl (SQL only) |
| `service.go` | `Service` interface + unexported impl (business rules; depends on this domain’s `Repository`, never another domain’s repo) |
| `handler.go` | Chi HTTP handlers; calls only `Service` |
| `routes.go` | `Routes()` (and `AuthRoutes` / `UserRoutes` for identity so `/auth` can keep a stricter rate limit) |

Wiring in `cmd/api`:

```text
repository → service → handler → handler.Routes() mounted on chi
```

JWT middleware depends on `middleware.TokenValidator` (implemented by `identity.Service`), not on the identity package directly, so there is no import cycle.

Planned domains (schema exists in `migrations/`; Go packages not added yet):

```text
internal/
  store/          → store, store_location, store_hours, store_media, store_kyb
  staff/          → staff_member
  catalog/        → category, brand, product, product_variant, product_image
  inventory/      → inventory
  order/          → orders, order_item, order_status_history
  finance/        → retailer_wallet, ledger_entry, settlement, bank_details
  review/         → review
  support/        → support_ticket, evidence
  verification/   → verification_request
```
