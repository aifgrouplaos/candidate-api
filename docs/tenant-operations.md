# Candidate tenant operations

These commands are for operators with database and private storage credentials.
They do not start an HTTP server or add candidate-facing endpoints. Run from
the repository root with the same database and bucket settings as the API.

## Local setup

1. Copy `.env.example` to ignored `.env`. Start `docker compose up -d postgres redis rustfs`.
   The Compose PostgreSQL port is **5435**; set `DB_PORT=5435`,
   `DB_ENABLED=true`, `MINIO_ENABLED=true`, and configure the private bucket.
   Enable JWT for candidate login (`JWT_ENABLED=true` and a signing secret of
   at least 32 bytes). Explicit `APP_ENV=development` skips rate limits locally.
2. Copy `tenants.example.json` to ignored `tenants.local.json`.
   The example contains **10 candidates = 10 isolated tenants**, each with its own
   Admin and initial Employee login (20 accounts total). Manifests accept 1–100
   tenants. Use globally distinct emails for every account. Tenant IDs are not part
   of the manifest: setup generates one per new Admin and reuses the existing tenant
   when the Admin email already exists. The manifest references password environment
   variable names; it contains no passwords. Supply each password in the ignored
   `.env`, process environment, or operator secret manager (8–72 bytes).
   Use test-only credentials separate from production and share them with each
   candidate through the team's private handoff channel.
3. Stop all API replicas before setup or reset, including local `go run` processes.
   This is a maintenance operation: concurrent requests, avatar uploads, or login
   could otherwise race with cleanup. Stopping replicas also drops all WebSocket
   connections, one-use tickets, presence, and typing state held in memory.
4. Set up tenants:

   ```sh
   go run ./cmd/api -tenant-setup tenants.local.json
   ```

   Or run `make tenant-setup`, which loads `.env` first.

   It prints one `<tenant ID>  <Admin email>` line per tenant. Keep the tenant IDs
   for later reset; rerunning setup prints the same IDs.

Setup creates all manifest tenants atomically, with one active Admin and one active,
login-enabled Employee each. Each Employee starts with code `EMP-0001`, the IT
department, and its Admin conversation. Repeating setup preserves existing account
IDs, profiles, passwords, and candidate work; an email in another tenant, an
inactive account, a different Admin, or a Deleted initial Employee causes a
conflict without partial account creation. Reset first to replace a Deleted
Employee. Repeating setup is not a password rotation mechanism.

Schema migration and idempotent department seeding run before the operation.
The five defaults are IT, Human Resources, Finance, Marketing, and Operations.
Task types (`feature`, `bug`) and priorities (`low`, `medium`, `high`, `critical`)
are shared code-defined lookups, available immediately through the API; they
require no additional database seed. Restart the API after successful setup.

## Reset and reuse

Stop **every** API replica, verify the selected database/bucket and the tenant ID
printed by setup for that Admin, then run:

```sh
go run ./cmd/api -tenant-reset 10000000-0000-4000-8000-000000000001
```

Or run `make tenant-reset TENANT=<tenant UUID>`. Add `ENV_FILE=.env.production`
to target production.

Reset prints the tenant's Admin email and requires typing it to confirm.
It requires configured private storage even if the tenant has no recorded
avatars. It requires exactly one existing Admin in the selected tenant. It:

1. Revokes all tenant sessions and refresh tokens, including the Admin's.
2. Lists and deletes every object under exactly `files/avatars/<tenant UUID>/`, including
   orphaned uploads, avatars belonging to Deleted Employees, historical object
   versions, and delete markers. Storage credentials must allow listing versions
   and deleting versions as well as ordinary objects.
3. In one database transaction, permanently removes tenant messages, conversations,
   Projects, Phases, Tasks, Employees (including Deleted Employees), and every
   login, session, and token row of the tenant, including the Admin. The tenant no
   longer exists. Shared lookups and other tenants stay intact.

A storage failure stops before database deletion; sessions stay revoked and the
records remain for retry. A database cleanup failure rolls back that cleanup.
Already deleted objects need no restoration: keep replicas stopped and rerun the
same reset until it succeeds. After a successful reset, the tenant ID is unknown and
a repeated reset is rejected. Object retention
or legal holds can prevent deletion; resolve those operator settings before retrying.
Reset removes persisted avatar objects; previously downloaded client copies
cannot be revoked.

To reuse a candidate, run tenant setup with the manifest again. It recreates the
Admin, initial Employee, and conversation under a **new** tenant ID while preserving
the other tenants' accounts and data. Restart API replicas only after reset/setup succeeds.

## Clear the whole database

Stop **every** API replica, then run:

```sh
make db-clear                                          # local docker-compose stack
make db-clear ENV_FILE=.env.production CONFIRM=production
```

It truncates every table in the database schema and deletes every object under
`files/avatars/` in the bucket. It asks you to type the database name, and also the
database host when that host is not `localhost` or `127.0.0.1`. Non-local hosts are
refused without `CONFIRM=production`, and get a `pg_dump` (through the
`postgres:18-alpine` Docker image) into ignored `backups/` first; a failed backup
deletes nothing. Avatars are not backed up. If avatar cleanup fails after the
database is cleared, rerun the same command. Afterwards run `make tenant-setup`
with the same `ENV_FILE`, which also reseeds departments, then restart the API.

`.env.production` must define every key: the Go command also reads `.env` and
uses its values for any key the selected file leaves unset.

## Repeatable verification

Run repository verification in temporary schemas of a **local test database**:

```powershell
$env:TEST_POSTGRES_DSN='host=localhost port=5435 user=postgres password=secret dbname=candidate-api_db sslmode=disable'
go test ./internal/tenant -count=1 -v
```

The tests verify ten-candidate setup creates ten tenants and twenty active accounts
without duplicating records on repetition. They also run setup twice, preserve
IDs/password hashes, populate two tenants
with sessions, messages and nested Projects, include a Deleted Employee, reset
one tenant twice, and verify that the other tenant and shared departments remain.
They also cover invalid/unknown tenants, conflicting emails and cleanup failure.
Without `TEST_POSTGRES_DSN`, the database tests skip.

For storage, candidate login, and reset verification against a running API, stop
`make run` and run `make e2e` against a local stack. It sets up the manifest
tenants, starts the API, and exercises the first two tenants' Admin, initial
Employee, a created Employee, avatar, chat, and Project flows. It also checks that
neither tenant can reach the other's records. Then it stops the API, resets the
first tenant, sets it up again, and restarts the API. Old tokens and the created
Employee must return `401`, and that tenant's avatar objects must be gone. Only the initial Employee and an empty conversation may
remain, and the second tenant's tokens and data must still work. See the README
for its environment guard.

No reset route is registered. Operator access is controlled by infrastructure
credentials and shell access, rather than candidate API authorization.
