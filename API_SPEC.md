# Candidate Take-Home API Specification

Status: implementation contract  
Audience: candidate frontend and API implementers  
Source decisions: `backend-api-requirements.md`, `Take Home  Frontend Engineer.pdf`, and the completed decisions in [Plan the candidate take-home API contract](https://github.com/aifgrouplaos/candidate-api/issues/1).

## 1. Scope and conventions

The API supports assessment tasks A1 (authentication and Employees), A2 (Employee–Admin chat), and A3 (nested Project forms). It is a Go service using the repository's `bkgo` conventions, PostgreSQL through `bkgo`'s database adapter, and a private self-hosted RustFS bucket for files.

The API does not create Admin accounts. An operator provisions one isolated candidate tenant with one Admin login and one login-enabled Employee account for each candidate. A candidate may create additional Employees through A1. Authentication determines the tenant; clients never choose a tenant in request payloads.

Unless an endpoint says otherwise:

- Base URL: `https://<host>/api/v1`.
- JSON fields use `camelCase`; IDs are opaque strings (UUID or ULID).
- JSON uses UTF-8. Timestamps are ISO 8601 UTC, such as `2026-10-05T08:30:00Z`. Calendar dates use `YYYY-MM-DD` and are not timestamps.
- Every request and response has `X-Request-Id`. The server generates one if the request does not provide one.
- Every successful response has a JSON envelope. A single result is `{ "data": <result> }`; a paginated result is `{ "data": [...], "meta": {...} }`; an empty successful result is `{ "data": null }`.
- The server enforces tenant and role authorization on every REST and WebSocket operation.
- REST list endpoints default to `page=1&limit=20`; `limit` is capped at 100. Lists use deterministic sorting. Chat messages use cursor pagination instead.
- The general rate limit is at least 100 requests per minute per authenticated user. `429` responses include `Retry-After`.

Browser clients may call the API from `http://localhost:3000` and `http://localhost:5173`. CORS allows `Authorization`, `Content-Type`, and `Idempotency-Key`. WebSocket connections enforce the same allowed Origins.

## 2. Authentication and authorization

### Account rules

- Login uses email and password. Email is globally unique because login has no tenant selector.
- Passwords are never returned. New Employee passwords must be at least 8 characters and are stored as secure password hashes.
- Access tokens are bearer tokens valid for 30 minutes. Refresh tokens are valid for 7 days and rotate on use.
- Logout revokes the submitted refresh token. A rotated or revoked refresh token cannot be reused.
- Roles are `admin` and `employee`. The API never accepts a caller-supplied role when creating an Employee.
- Admins can manage Employees and tenant-owned records in their own candidate tenant only. Employees can read their own profile and update only `fullName`, `phone`, and `avatarUrl`.
- Admin accounts cannot be created, edited, have their role changed, or be deleted through this API.
- Every owner, assignee, conversation, file, and Project lookup is checked against the authenticated tenant. Cross-tenant access is denied.

### Endpoints

| Method and path | Access | Contract |
| --- | --- | --- |
| `POST /auth/login` | Public | Accepts `{ "email", "password" }`; returns access and refresh tokens plus the authenticated user. Invalid credentials return `401` without identifying which field was wrong. |
| `POST /auth/refresh` | Public | Accepts `{ "refreshToken" }`; returns a new access token and a rotated refresh token. Expired, revoked, or reused tokens return `401`. |
| `POST /auth/logout` | Authenticated | Accepts `{ "refreshToken" }`, revokes it, and returns `{ "data": null }`. |

Login response:

```json
{
  "data": {
    "accessToken": "<opaque-or-signed-token>",
    "refreshToken": "<opaque-token>",
    "tokenType": "Bearer",
    "expiresIn": 1800,
    "user": { "id": "u_01", "email": "admin@example.test", "role": "admin" }
  }
}
```

Send the access token as `Authorization: Bearer <accessToken>` on protected REST calls. WebSocket clients obtain a one-use connection ticket as described in section 5; they do not put an access token in the URL.

## 3. Shared error contract

Errors have this shape, and `requestId` matches the `X-Request-Id` response header:

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "The request is invalid.",
    "details": [{ "field": "phases[0].tasks[1].severity", "message": "Severity is required for bug tasks." }]
  },
  "requestId": "req_01H..."
}
```

| HTTP | Codes | Use |
| --- | --- | --- |
| `400` | `BAD_REQUEST` | Malformed JSON or malformed request syntax. |
| `401` | `UNAUTHORIZED`, `TOKEN_EXPIRED` | Missing, invalid, expired, revoked, or reused credentials. |
| `403` | `FORBIDDEN` | The caller lacks the required role or tenant ownership. |
| `404` | `NOT_FOUND` | The resource does not exist in the caller's authorized scope. |
| `409` | `CONFLICT`, `VERSION_CONFLICT`, `IDEMPOTENCY_CONFLICT` | Duplicate unique value, stale resource version, or an idempotency key reused with a different payload. |
| `422` | `VALIDATION_ERROR` | One or more field validations failed. Return every discovered error with a full nested field path. |
| `429` | `RATE_LIMITED` | Rate limit exceeded; include `Retry-After`. |
| `500` | `INTERNAL_ERROR` | Unexpected server failure without internal details. |

## 4. A1: Employees and departments

### Employee resource

```json
{
  "id": "e_001",
  "employeeCode": "EMP-0001",
  "fullName": "Somchai Example",
  "email": "employee@example.test",
  "phone": "020 5555 5555",
  "department": { "id": "d_1", "name": "IT" },
  "position": "Developer",
  "status": "active",
  "hireDate": "2024-03-01",
  "avatarUrl": null,
  "version": 1,
  "createdAt": "2026-10-05T08:30:00Z",
  "updatedAt": "2026-10-05T08:30:00Z"
}
```

`status` is `active`, `inactive`, or `on_leave`. Employee responses never contain a password. When an avatar exists, `avatarUrl` is a short-lived presigned download URL issued only after the API has checked that the caller may access that Employee. The bucket stays private; clients must not persist the URL as a permanent identifier.

| Method and path | Access | Contract |
| --- | --- | --- |
| `GET /employees` | Admin | Paginated tenant Employee list. Query: `page`, `limit`, `search` (name/email/code), `departmentId`, comma-separated `status`, `sortBy` (`fullName`, `hireDate`, `createdAt`), `sortOrder` (`asc`, `desc`). |
| `GET /employees/{id}` | Admin or that Employee | Reads an Employee in the caller's tenant; Employees may read only themselves. |
| `POST /employees` | Admin | Creates an Employee and its login account atomically, plus that Employee's one-to-one chat conversation. The request includes Employee fields and `password`; the role is always `employee`. |
| `PATCH /employees/{id}` | Admin or that Employee | Admin may update `fullName`, `email`, `phone`, `departmentId`, `position`, `status`, and `hireDate`. An Employee may update only `fullName`, `phone`, and `avatarUrl`. Neither role may change `role` through this endpoint. Include the current `version`; a stale version returns `409 VERSION_CONFLICT`. |
| `DELETE /employees/{id}` | Admin | Soft-deletes/disables the Employee login and retains the Employee record and chat history. Admin accounts cannot be deleted. |
| `POST /employees/{id}/avatar` | Admin or that Employee | Uploads `multipart/form-data`, maximum 2 MB, JPG/PNG/WebP. The API validates content and authorization before writing to private RustFS. |
| `GET /departments` | Authenticated | Returns the shared department lookup list. |

`POST /employees` request example:

```json
{
  "fullName": "Somchai Example",
  "email": "employee@example.test",
  "password": "<candidate-provided-password>",
  "phone": "020 5555 5555",
  "departmentId": "d_1",
  "position": "Developer",
  "status": "active",
  "hireDate": "2026-10-05"
}
```

Validation: `fullName` is required and 2–100 characters; email must be valid and globally unique; phone must be a valid phone number; `departmentId` must exist; `hireDate` cannot be in the future. Duplicate email returns `409`; field validation returns `422` with `details[]`. The tenant Employee limit is at most 200 login-enabled Employees.

## 5. A2: Employee–Admin chat

### Conversation and message rules

- Each Employee has exactly one conversation with the Admin who owns that Employee. It is created with the Employee so it appears in the Admin inbox immediately.
- Only the Employee and that Employee's owning Admin may access the conversation. Employees cannot chat with other Employees. Tenant checks apply to REST and WebSocket operations.
- Message text is plain text, 1–2,000 characters. Clients render it as text, not HTML.
- `sent` means saved by the server. `delivered` means at least one recipient session confirmed receipt. `read` means the recipient marked it seen.
- Unread counts include only messages from the other participant. Read and delivery positions are shared across that participant's authenticated sessions and advance monotonically.
- The server assigns a stable, increasing `sequence` within a conversation. Message IDs are opaque and do not define order. Responses are ordered oldest to newest.

Conversation response:

```json
{
  "id": "c_001",
  "employee": { "id": "e_001", "fullName": "Somchai Example", "avatarUrl": null },
  "lastMessage": null,
  "unreadCount": 0,
  "updatedAt": "2026-10-05T08:30:00Z"
}
```

Message response:

```json
{
  "id": "m_100",
  "clientMessageId": "8f14e45f-...",
  "conversationId": "c_001",
  "sequence": 100,
  "sender": { "id": "u_1", "fullName": "Admin Example", "role": "admin" },
  "text": "Hello",
  "status": "sent",
  "createdAt": "2026-10-05T08:30:00Z",
  "readAt": null
}
```

### REST endpoints

| Method and path | Access | Contract |
| --- | --- | --- |
| `GET /chat/conversations` | Authenticated | Admin sees tenant conversations; Employee sees their single conversation. Supports `page`, `limit`, `search`, `unreadOnly=true`; Admin inbox sorts by most recently updated. |
| `POST /chat/conversations` | Employee | Returns the Employee's existing conversation (idempotent; normally already created by `POST /employees`). |
| `GET /chat/conversations/{id}` | Participant | Returns the authorized conversation. |
| `GET /chat/conversations/{id}/messages` | Participant | Query `limit` (default 30, max 100), `before` (exclusive older cursor), or `after` (exclusive newer cursor). Returns ascending messages and `{ "hasMoreBefore", "hasMoreAfter", "nextBefore" }` metadata. |
| `POST /chat/conversations/{id}/messages` | Participant | Accepts `{ "clientMessageId", "text" }`. Returns the saved Message. Repeating a `clientMessageId` with the same payload returns the original Message; using it with a different payload returns `409 IDEMPOTENCY_CONFLICT`. |
| `POST /chat/conversations/{id}/read` | Participant | Accepts `{ "lastReadMessageId" }`; advances the participant's shared read position through that message. |
| `GET /chat/unread-count` | Authenticated | Returns `{ "total": <count> }` for the caller. |
| `POST /chat/ws-ticket` | Authenticated | Returns a single-use WebSocket ticket valid for 30–60 seconds. |

All chat message cursor operations are exclusive. Reconnect clients request `after=<last contiguous message ID>` to recover every later message. The server uses conversation sequence order to ensure there are no gaps in the sync result.

### WebSocket

Connect to `wss://<host>/ws/chat?ticket=<single-use-ticket>` after obtaining a ticket. The server validates the ticket and Origin at connection time; access tokens must not appear in the URL. The server sends a ping about every 25 seconds; clients respond with `pong`.

Every event uses `{ "type": "<event>", "data": {...}, "ts": "<UTC timestamp>" }`.

Client → server:

| Type | Data | Effect |
| --- | --- | --- |
| `message.delivered` | `{ "conversationId", "lastContiguousMessageId" }` | Confirms receipt through the last contiguous message seen by this session. The server advances the shared delivery position and emits status changes. |
| `message.read` | `{ "conversationId", "lastReadMessageId" }` | Marks messages through the last message shown as read. Equivalent to the REST `/read` operation. |
| `typing` | `{ "conversationId", "isTyping" }` | Temporarily relays typing state to the other participant; it is not stored. |
| `ping` | `{}` | Optional client heartbeat. |

Server → client:

| Type | Data | Effect |
| --- | --- | --- |
| `message.new` | `Message` | Broadcasts a newly saved message to all authenticated sessions of both participants. Creation remains a REST operation. |
| `message.status` | `{ "messageId", "conversationId", "status", "readAt" }` | Reports `delivered` or `read` state changes. |
| `conversation.updated` | `Conversation` | Updates inbox preview and per-conversation unread count. |
| `unread.updated` | `{ "total" }` | Updates the aggregate unread badge. |
| `typing` | `{ "conversationId", "userId", "isTyping" }` | Reports temporary typing state from the other participant. |
| `presence` | `{ "userId", "online", "lastSeenAt" }` | Online means at least one authenticated chat session is active. Presence is temporary. |
| `error` | `{ "code", "message" }` | Reports a connection/event error. |

When a conversation is open, the client marks as read through the last message displayed. Any authenticated session's delivery/read acknowledgement advances the participant's shared position. After reconnect, the client syncs with the REST `after` cursor before relying on live events.

## 6. A3: Project → Phase → Task

### Domain and validation

An Admin creates one Project with nested Phases and Tasks in a single request. `ownerId` identifies the Employee who owns the Project; `assigneeId` identifies the Employee assigned to a Task. Both must refer to active Employees in the authenticated Admin's tenant. The tenant is never supplied by the request.

Lookup values:

- Task types: `feature`, `bug`.
- Priorities: `low`, `medium`, `high`, `critical`.
- Bug severity: `minor`, `major`, `critical`. A `bug` requires severity; other types require `severity: null`.

Dates are inclusive `YYYY-MM-DD` values. A Phase's dates must fit within its Project dates; a Task's `dueDate` must fit within its Phase dates.

| Method and path | Access | Contract |
| --- | --- | --- |
| `POST /projects` | Admin | Creates Project, Phases, and Tasks atomically. Requires `Idempotency-Key`. |
| `GET /projects` | Authenticated | Returns only Projects in the caller's tenant, paginated, with optional `search`. |
| `GET /projects/{id}` | Authenticated | Returns the tenant Project with all Phases and Tasks. |
| `DELETE /projects/{id}` | Admin | Deletes a tenant Project for assessment cleanup. |
| `GET /lookups/task-types` | Authenticated | Returns task type choices. |
| `GET /lookups/priorities` | Authenticated | Returns priority choices. |

`POST /projects` body:

```json
{
  "name": "HR system upgrade",
  "code": "PRJ-2026-001",
  "description": "Project description",
  "ownerId": "e_001",
  "startDate": "2026-11-01",
  "endDate": "2027-03-31",
  "phases": [
    {
      "name": "Requirements",
      "order": 1,
      "startDate": "2026-11-01",
      "endDate": "2026-12-15",
      "tasks": [
        {
          "title": "Interview users",
          "type": "feature",
          "priority": "high",
          "assigneeId": "e_002",
          "estimateHours": 16,
          "dueDate": "2026-11-20",
          "severity": null
        }
      ]
    }
  ]
}
```

The `201` response returns the same nested shape with IDs for the Project, every Phase, and every Task, plus `createdAt`, `updatedAt`, and `version`, inside the standard `{ "data": ... }` envelope.

Validation rules:

- Project `name` is required and 3–100 characters; `code` is unique within the tenant; `ownerId` must exist and belong to the tenant; `endDate` must be on or after `startDate`; `phases` contains 1–20 items.
- Phase `name` is required; `order` is unique within its Project; phase dates fit within the Project; `tasks` contains 1–100 items.
- Task `title` is required; `type` and `priority` must be valid lookup values; `assigneeId` must identify an active Employee in the tenant; `estimateHours` is greater than 0 and at most 999; `dueDate` fits within the Phase.
- The total number of Tasks in one request is at least 500. Any failure rolls back the entire Project, Phase, and Task write.
- Return all validation errors in one `422` response. Nested field paths use bracket notation, for example `phases[0].tasks[1].severity`.
- `Idempotency-Key` makes retries safe. The same key and payload return the original result; the same key with a different payload returns `409 IDEMPOTENCY_CONFLICT`.

## 7. Candidate readiness and operations

- Provide at least two isolated candidate tenants, each with an Admin login and a login-enabled Employee account. Credentials are test-only, distinct from production credentials, and shared with candidates outside this API.
- Candidate-created Employees can log in immediately and chat with their owning Admin.
- Provide an operator-only reset procedure that revokes tenant sessions and removes that tenant's Employees, conversations, and Projects before the tenant is reused. Do not expose this operation as a candidate API endpoint.
- Seed shared departments (at least five), Task types, and priorities. Optional sample Employee rows used for list/search tests must not have login accounts.
- Publish OpenAPI/Swagger or a Postman collection for the REST contract and document every WebSocket event above.
- Deployment is expected to be externally reachable over HTTPS/WSS. The API owner supplies ArgoCD registration, ingress, hostname, TLS, and deployment-specific database selection; those platform choices are outside this API contract.

## 8. Explicitly out of scope

- Building the candidate frontend.
- Chat attachments and other bonus-only frontend/API features.
- Candidate-facing Admin creation or tenant selection.
- Deployment platform details owned by the API owner (ArgoCD registration, replicas, ingress, hostname, and TLS configuration).
