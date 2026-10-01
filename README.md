# Candidate API

Backend API for AIF Group Laos's Frontend Engineer take-home assessment. Candidates run their frontend locally and connect to a shared, hosted API to complete three exercises: employee management, real-time chat, and a dynamic nested form.

## Current status

The repository contains the assessment materials and an initialized Go API scaffold. The assessment endpoints and deployment setup are still to be implemented. The features below describe the planned scope.

## Run locally

Requires Go 1.22+ and Docker Compose. The scaffold uses Fiber and PostgreSQL; PostgreSQL is enabled by default. Redis is required when authentication routes are enabled. MinIO and JWT remain optional for local development.

```sh
cp .env.example .env
docker compose up -d postgres redis
go mod tidy
go run cmd/api/main.go
```

The API listens on `http://localhost:8080`. Check `GET /health` for the scaffold health response. `.env.example` documents the app and adapter settings. Set `JWT_ENABLED=true` and a `JWT_SECRET` of at least 32 bytes when enabling JWT-protected routes. Production also requires Redis and explicit `TRUSTED_PROXIES` IPs/CIDRs for ingress client-IP rate limits. `ALLOWED_ORIGINS` overrides the default browser origins (`http://localhost:3000`, `http://localhost:5173`).

## Project scope

| Exercise | Capability | Main requirements |
| --- | --- | --- |
| A1 | Authentication and employee management | Login, rotating refresh tokens, role-based access, employee CRUD, search, filtering, sorting, pagination, and avatar uploads |
| A2 | Employee–admin chat | One conversation per employee with their creating admin, message history, real-time updates, read status, unread counts, typing, presence, and reconnect synchronization |
| A3 | Three-level dynamic form | Submit a nested structure in one request, validate all levels, save atomically, and retrieve the result for verification |

A3 currently uses **project → phases → tasks** as its example domain; the final domain remains to be confirmed. All three exercises are required.

## Candidate workflow and data isolation

1. The team provisions an admin account directly in the database, with a separate account recommended for each candidate.
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
