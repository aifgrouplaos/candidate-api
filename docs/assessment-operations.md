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
2. Copy `assessment.example.json` to ignored `assessment.local.json`.
   The example contains **10 candidates = 10 isolated tenants**, each with its own
   Admin and initial Employee login (20 accounts total). Manifests accept 1–100
   tenants. Choose a distinct canonical, nonzero UUID per candidate and globally
   distinct emails for every account.
   Keep these UUIDs for later reset. The manifest references password environment
   variable names; it contains no passwords. Supply each password in the ignored
   `.env`, process environment, or operator secret manager (8–72 bytes).
   Use test-only credentials separate from production and share them with each
   candidate through the team's private handoff channel.
3. Stop all API replicas before setup or reset, including local `go run` processes.
   This is a maintenance operation: concurrent requests, avatar uploads, or login
   could otherwise race with cleanup. Stopping replicas also drops all WebSocket
   connections, one-use tickets, presence, and typing state held in memory.
4. Provision:

   ```sh
   go run ./cmd/api -assessment-provision assessment.local.json
   ```

Provision creates all manifest tenants atomically, with one active Admin and one active,
login-enabled Employee each. Each Employee starts with code `EMP-0001`, the IT
department, and its Admin conversation. Repeating setup preserves existing account
IDs, profiles, passwords, and assessment work; an email in another tenant, an
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
against the manifest, then run:

```sh
go run ./cmd/api -assessment-reset 10000000-0000-4000-8000-000000000001
```

Reset requires configured private storage even if the tenant has no recorded
avatars. It requires exactly one existing Admin in the selected tenant. It:

1. Revokes all tenant sessions and refresh tokens, including the Admin's.
2. Lists and deletes every object under exactly `avatars/<tenant UUID>/`, including
   orphaned uploads, avatars belonging to Deleted Employees, historical object
   versions, and delete markers. Storage credentials must allow listing versions
   and deleting versions as well as ordinary objects.
3. In one database transaction, permanently removes tenant messages, conversations,
   Projects, Phases, Tasks, Employees (including Deleted Employees), and Employee
   login/session/token rows. The Admin account and its credentials remain; revoked
   Admin sessions remain revoked. Shared lookups and other tenants stay intact.

A storage failure stops before database deletion; sessions stay revoked and the
records remain for retry. A database cleanup failure rolls back that cleanup.
Already deleted objects need no restoration: keep replicas stopped and rerun the
same reset until it succeeds. Repeated successful resets are safe. Object retention
or legal holds can prevent deletion; resolve those operator settings before retrying.
Reset removes persisted avatar objects; previously downloaded client copies
cannot be revoked.

To reuse a tenant, run the provisioning manifest again. It recreates
the missing initial Employee and conversation while preserving the other tenant's
accounts and data. The retained Admin needs to sign in again. Restart API replicas
only after reset/setup succeeds.

## Repeatable verification

Run repository verification in temporary schemas of a **local test database**:

```powershell
$env:TEST_POSTGRES_DSN='host=localhost port=5435 user=postgres password=secret dbname=candidate-api_db sslmode=disable'
go test ./internal/tenant -count=1 -v
```

The tests verify ten-candidate setup creates ten tenants and twenty active accounts
without duplicating records on repetition. They also provision twice, preserve
IDs/password hashes, populate two tenants
with sessions, messages and nested Projects, include a Deleted Employee, reset
one tenant twice, and verify that the other tenant and shared departments remain.
They also cover invalid/unknown tenants, conflicting emails and cleanup failure.
Without `TEST_POSTGRES_DSN`, the database tests skip.

For storage and candidate login verification in a disposable assessment environment:

1. Provision the manifest twice; log in as every configured account. Verify five departments,
   task types and priorities, and each Employee's initial conversation.
2. Upload an avatar in each tenant, send a chat message, and create a Project with a
   Phase and Task in each tenant. Keep both tenants' tokens and Project IDs.
   Optionally put an orphan object under `avatars/<first tenant UUID>/` using the
   operator's S3 client; reset must remove it too.
3. Stop all replicas. Reset the first tenant twice. With an S3 client, verify its
   avatar prefix is empty while the second tenant's objects remain unchanged.
4. Restart the API. The first tenant's old access/refresh tokens must return `401`,
   including its Admin tokens. Its retained Admin can log in again and sees empty
   Employee, conversation and Project lists. Its former Employee cannot log in.
   The second tenant's existing tokens and data must still work.
5. Stop replicas, provision again, then restart. The first tenant's initial Employee
   can log in and has one empty conversation, while the second tenant is unchanged.

No reset route is registered. Operator access is controlled by infrastructure
credentials and shell access, rather than candidate API authorization.
