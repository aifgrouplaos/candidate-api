# Candidate API

Backend API for AIF Group Laos's Frontend Engineer take-home assessment. Candidates run their frontend locally and connect to a shared, hosted API to complete three exercises: employee management, real-time chat, and a dynamic nested form.

## Current status

The A1, A2, and A3 endpoints, chat WebSocket, candidate tenant setup/reset, and API docs are implemented. Deployment (ingress, hostname, TLS) is owned by the API owner.

## Run locally

Requires Go 1.22+ and Docker Compose. The API uses Fiber, PostgreSQL, Redis-backed rate limits, JWT, and a private RustFS bucket for avatars.

```sh
cp .env.example .env
docker compose up -d postgres redis rustfs
```

In `.env`, set `JWT_ENABLED=true`, a `JWT_SECRET` of at least 32 bytes, `MINIO_ENABLED=true`, and the `CANDIDATE_*_PASSWORD` values for your manifest. `.env.example` already points at the Compose ports (PostgreSQL on `5435`). An explicit `APP_ENV=development` skips rate limits, so Redis is optional then. Create candidate tenants, then start the API:

```sh
cp tenants.example.json tenants.local.json   # optional: trim to the tenants you need
make tenant-setup
make run
```

The API listens on `http://localhost:8080` (`GET /health`). Sign in with a manifest account at `POST /api/v1/auth/login`.

- `GET /docs` serves Swagger UI over the REST contract (`pkg/apidocs/openapi.yaml`, raw at `GET /openapi.yaml`); use **Authorize** with the `accessToken`. Its description documents the chat WebSocket connection, every event payload, and close codes.
- `tools/chat-ui/index.html` is a manual two-browser chat client for the same API.
- `ALLOWED_ORIGINS` overrides the default browser origins (`http://localhost:3000`, `http://localhost:5173`) for both CORS and WebSocket.
- Production also requires Redis, private object storage, and explicit `TRUSTED_PROXIES` IPs/CIDRs for ingress client-IP rate limits.

## Verify

```sh
make test   # unit tests; set TEST_POSTGRES_DSN to include repository tests
make e2e    # stop `make run` first
```

`make e2e` builds and starts the API on `APP_PORT`, runs setup, and drives the documented flows for the first two `MANIFEST` tenants against it. Those flows are login, `/auth/me`, Employee create/search/update with version conflicts, avatar upload and presigned download, Project create with idempotent retry, and chat over REST and WebSocket. It checks that neither tenant can read or change the other's records, stops the API, resets the first tenant, runs setup again, and confirms only that tenant was cleared. It deletes tenant data, so it refuses to run unless `APP_ENV=development` and `DB_HOST` and `MINIO_ENDPOINT` are local. Use `ENV_FILE=<file>` and `MANIFEST=<file>` to point it at a dedicated local env and manifest.

## Create a candidate Admin

With PostgreSQL running and `DB_ENABLED=true` in the environment or `.env`, run:

```sh
go run ./cmd/create-admin
```

The command prompts for the Admin's email and name, then asks for a password twice without echoing it. It generates a new candidate tenant UUID and creates an active Admin account in that tenant. Save the printed tenant ID with the candidate's setup information. Email addresses are globally unique; if an address is already used, the command exits without changing the existing account. The Admin can create Employees through the API after signing in.

## Project scope

For repeatable candidate setup and tenant reset, follow
[the tenant operations runbook](docs/tenant-operations.md).
The operator commands run from `cmd/api` and expose no HTTP reset endpoint.

| Exercise | Capability | Main requirements |
| --- | --- | --- |
| A1 | Authentication and employee management | Login, rotating refresh tokens, role-based access, employee CRUD, search, filtering, sorting, pagination, and avatar uploads |
| A2 | Employee–admin chat | One conversation per employee with their creating admin, message history, real-time updates, read status, unread counts, typing, presence, and reconnect synchronization |
| A3 | Three-level dynamic form | Submit a nested structure in one request, validate all levels, save atomically, and retrieve the result for verification |

A3 currently uses **project → phases → tasks** as its example domain; the final domain remains to be confirmed. All three exercises are required.

## Candidate workflow and data isolation

1. The team runs `go run ./cmd/create-admin` to create an admin account in a new candidate tenant, with a separate account recommended for each candidate.
2. The candidate signs in as that admin and creates employees through A1. Each created employee receives a login account and a conversation with that admin.
3. The candidate signs in as an employee in another browser session to test chat and employee permissions.
4. The candidate submits the nested form and retrieves the saved data to verify it.

Employees, conversations, and projects belong to the creating admin's tenant. Access must stay within that tenant, including real-time events. Admin accounts cannot be created, deleted, or modified through the employee APIs. Test data must be resettable between assessments.

## API expectations

- Versioned REST API under `/api/v1`, using UTF-8 JSON, `camelCase` fields, string IDs, and UTC timestamps.
- Bearer-token authentication with `admin` and `employee` roles.
- Local frontend access from `http://localhost:3000` and `http://localhost:5173`.
- Consistent error responses with field-level validation details and request IDs.
- Page-based list pagination, with cursor-based pagination for chat history.
- Conflict detection for concurrent updates and idempotency for message sends and nested-form submissions.
- Real-time chat through WebSocket, or SSE if agreed with the frontend team.

The handoff must include a reachable HTTPS API, API documentation through Swagger/OpenAPI or a Postman collection, real-time event documentation, test accounts, lookup seed data, and a data-reset procedure.

## Documentation

- [Backend API requirements (Lao)](backend-api-requirements.md): endpoint contracts, validation rules, permissions, delivery checklist, and open questions.
- [Frontend Engineer take-home assessment](Take%20Home%20%20Frontend%20Engineer.pdf): assessment brief.
- [Agent instructions](AGENTS.md): repository conventions for engineering skills.
- [GitHub Issues](https://github.com/aifgrouplaos/candidate-api/issues): project issues and specifications.

Domain documentation follows a single-context layout: a root `CONTEXT.md` and `docs/adr/`, created as terminology and architectural decisions are resolved.

## Planned delivery order

1. Authentication and employee management (A1), with API documentation.
2. Nested-form submission and lookups (A3).
3. Chat REST endpoints, followed by real-time delivery (A2).
4. Seed data, test-account verification, tenant-isolation checks, and final handoff documentation.

Before implementation, confirm the chat transport, employee directory visibility, A3 domain, number of concurrent candidates, and API handoff date. See the backend requirements for details.
