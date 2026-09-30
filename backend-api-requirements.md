# ຂໍ້ກຳນົດ API ສຳລັບທີມ Backend
**ໂປຣເຈັກ:** ໂຈດທົດສອບ Frontend Engineer (Take-home) ຂໍ້ A1, A2, A3

---

## 0. ພາບລວມ

ຜູ້ສະໝັກ (Frontend) ຈະ run web ໃນ local ແລ້ວເຊື່ອມຕໍ່ API ຂອງພວກເຮົາ. Backend ຕ້ອງສະໜອງ API ຕໍ່ໄປນີ້:

| ໂຈດ | ສິ່ງທີ່ Backend ຕ້ອງສ້າງ | ບຸລິມະສິດ |
|---|---|---|
| A1 | Auth + Employee CRUD | P0 (ຕ້ອງມີກ່ອນ) |
| A2 | Chat (REST + WebSocket ຫຼື SSE) | P0 |
| A3 | POST ຟອມ nested 3 ຊັ້ນ (+ GET ສຳລັບກວດສອບ) | P0 |

**ສິ່ງທີ່ຕ້ອງມີເພີ່ມ:** Swagger/OpenAPI ຫຼື Postman collection, ຂໍ້ມູນ seed, ບັນຊີທົດສອບ, ແລະ ເຊີບເວີທີ່ຜູ້ສະໝັກເຂົ້າເຖິງໄດ້ຈາກພາຍນອກ.

---

## 1. ຂໍ້ກຳນົດທົ່ວໄປ (ທຸກ API)

### 1.1 ພື້ນຖານ
- Base URL: `https://<host>/api/v1`
- Content-Type: `application/json; charset=utf-8` (ຮອງຮັບຕົວອັກສອນລາວ UTF-8)
- ເວລາທັງໝົດເປັນ **ISO 8601 UTC** ເຊັ່ນ `2026-10-05T08:30:00Z`
- ID ເປັນ string (UUID ຫຼື ULID)
- ຊື່ field ເປັນ `camelCase`
- **CORS:** ອະນຸຍາດ `http://localhost:3000`, `http://localhost:5173` (ແລະ `Authorization`, `Content-Type`, `Idempotency-Key` headers) ທັງ REST ແລະ WebSocket (Origin check)

### 1.2 Authentication
- ໃຊ້ Bearer token: `Authorization: Bearer <accessToken>`
- Access token ອາຍຸ 15–30 ນາທີ, Refresh token ອາຍຸ 7 ວັນ (ແບບ rotate: ໃຊ້ແລ້ວອອກໃໝ່)
- ບົດບາດ (role): `admin` ແລະ `employee`

### 1.3 ຮູບແບບ Error (ຕ້ອງເປັນຮູບແບບດຽວກັນທຸກ endpoint)
```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "ຂໍ້ມູນບໍ່ຖືກຕ້ອງ",
    "details": [
      { "field": "email", "message": "ຮູບແບບອີເມວບໍ່ຖືກຕ້ອງ" }
    ]
  },
  "requestId": "req_01H..."
}
```

| HTTP | code | ເມື່ອໃດ |
|---|---|---|
| 400 | `BAD_REQUEST` | JSON ຜິດຮູບແບບ |
| 401 | `UNAUTHORIZED` / `TOKEN_EXPIRED` | ບໍ່ມີ token / token ໝົດອາຍຸ |
| 403 | `FORBIDDEN` | ບໍ່ມີສິດ |
| 404 | `NOT_FOUND` | ບໍ່ພົບຂໍ້ມູນ |
| 409 | `CONFLICT` | ຂໍ້ມູນຊ້ຳ (ເຊັ່ນ ອີເມວຊ້ຳ) ຫຼື version ບໍ່ກົງ |
| 422 | `VALIDATION_ERROR` | validation ບໍ່ຜ່ານ (ມີ `details[]` ລະບຸ field) |
| 429 | `RATE_LIMITED` | ເກີນ limit (ໃສ່ header `Retry-After`) |
| 500 | `INTERNAL_ERROR` | ຂໍ້ຜິດພາດຂອງ server |

### 1.4 Pagination (ສຳລັບລາຍການ)
Query: `page` (ເລີ່ມ 1), `limit` (ຄ່າເລີ່ມຕົ້ນ 20, ສູງສຸດ 100). Response:
```json
{
  "data": [ ],
  "meta": { "page": 1, "limit": 20, "total": 245, "totalPages": 13 }
}
```
> ຂໍ້ຍົກເວັ້ນ: ຂໍ້ຄວາມ chat ໃຊ້ **cursor pagination** (ເບິ່ງຫົວຂໍ້ 3).

### 1.5 ອື່ນໆ
- Rate limit ທົ່ວໄປ ≥ 100 req/ນາທີ/ຜູ້ໃຊ້ (ບໍ່ໃຫ້ຕ່ຳເກີນໄປຈົນຜູ້ສະໝັກທົດສອບບໍ່ໄດ້)
- ທຸກ response ໃສ່ `X-Request-Id`
- ແກ້ໄຂພ້ອມກັນ: ຮອງຮັບ `version` (ຫຼື `updatedAt`) ໃນ PUT/PATCH ແລ້ວຕອບ `409` ຖ້າບໍ່ກົງ (ຊ່ວຍໃຫ້ຜູ້ສະໝັກສະແດງການຈັດການ conflict)

### 1.6 ບັນຊີທົດສອບ ແລະ ການແຍກຂໍ້ມູນຜູ້ສະໝັກ (ສຳຄັນ)

**ວິທີການໃຊ້ງານ:** ທີມເຮົາສ້າງບັນຊີ **Admin** ໄວ້ໃນ DB ໂດຍກົງ (ບໍ່ມີ API ສ້າງ Admin). ຜູ້ສະໝັກ login ດ້ວຍ Admin ນັ້ນ → **ສ້າງພະນັກງານເອງ** ຜ່ານ A1 → login ເປັນພະນັກງານທີ່ສ້າງ (ອີກ browser/incognito) → ສົນທະນາກັບ Admin ໄດ້. ສະນັ້ນຕ້ອງຮັບປະກັນຕາມນີ້:

1. **ສ້າງພະນັກງານ = ສ້າງບັນຊີ login ນຳ:** `POST /employees` ຮັບ `password` (ຂັ້ນຕ່ຳ 8 ຕົວ) ແລ້ວສ້າງ user ທີ່ `role = employee` ທັນທີ; ພະນັກງານ login ດ້ວຍ `email` + `password` ນັ້ນໄດ້ເລີຍ. API ຕ້ອງບໍ່ສາມາດສ້າງ `admin` ໄດ້.
2. **ຫ້າມຜູ້ສະໝັກແຕະບັນຊີ Admin:** ລຶບ/ແກ້ role/ປ່ຽນລະຫັດຜ່ານຂອງ Admin ຜ່ານ API ບໍ່ໄດ້ (`403`). Admin ບໍ່ຢູ່ໃນລາຍການ `/employees`.
3. **Admin 1 ບັນຊີ ຕໍ່ ຜູ້ສະໝັກ 1 ຄົນ (ແນະນຳ):** ເຊັ່ນ `candidate01@…`, `candidate02@…`. ຖ້າໃຫ້ຫຼາຍຄົນໃຊ້ Admin ດຽວກັນ ຂໍ້ມູນຈະປົນກັນ (ລຶບພະນັກງານຂອງກັນ, chat ປົນ, ຜົນ test ບໍ່ຖືກຕ້ອງ).
4. **ແຍກຂໍ້ມູນຕາມ Admin (tenant):** ທຸກ record (`employees`, `conversations`, `projects`) ມີ `ownerAdminId` (ຫຼື `tenantId`). Admin ເຫັນ ແລະ ຈັດການໄດ້ສະເພາະຂໍ້ມູນຂອງຕົນ (ຄຳວ່າ "admin: ທັງໝົດ" ໃນທຸກຕາຕະລາງດ້ານລຸ່ມ ໝາຍເຖິງທັງໝົດພາຍໃນ tenant ຂອງຕົນເອງ). ພະນັກງານຂອງ Admin A ຕ້ອງບໍ່ເຫັນ ຫຼື chat ກັບ Admin B ໄດ້.
5. **ຄວາມປອດໄພຂອງບັນຊີທົດສອບ:** ໃຊ້ບັນຊີ/ລະຫັດຜ່ານທີ່ສ້າງໃໝ່ສະເພາະການທົດສອບ ບໍ່ຊ້ຳກັບລະບົບຈິງ, ໃຊ້ໃນສະພາບແວດລ້ອມ test ເທົ່ານັ້ນ, ແລະ ປ່ຽນ/ປິດບັນຊີຫຼັງຜູ້ສະໝັກສົ່ງງານແລ້ວ.
6. **ລ້າງຂໍ້ມູນ:** ມີ script ລຶບ employees/conversations/projects ຂອງ Admin ນັ້ນ ເພື່ອ reset ກ່ອນສົ່ງໃຫ້ຜູ້ສະໝັກຄົນຕໍ່ໄປ. ຈຳກັດຈຳນວນພະນັກງານທີ່ສ້າງໄດ້ຕໍ່ Admin (ເຊັ່ນ ≤ 200) ເພື່ອປ້ອງກັນການໃຊ້ຜິດ.

---

## 2. A1: Auth + Employee

### 2.1 Auth

| Method | Path | ລາຍລະອຽດ | Auth |
|---|---|---|---|
| POST | `/auth/login` | ເຂົ້າສູ່ລະບົບ | ບໍ່ຕ້ອງ |
| POST | `/auth/refresh` | ຂໍ access token ໃໝ່ | ບໍ່ຕ້ອງ (ໃຊ້ refreshToken) |
| POST | `/auth/logout` | ຖອນ refresh token | ຕ້ອງ |
| GET | `/auth/me` | ຂໍ້ມູນຜູ້ໃຊ້ປັດຈຸບັນ + role | ຕ້ອງ |

**POST `/auth/login`**
```json
// Request
{ "email": "admin@example.com", "password": "********" }

// Response 200
{
  "accessToken": "...",
  "refreshToken": "...",
  "expiresIn": 1800,
  "user": { "id": "u_1", "email": "admin@example.com", "fullName": "...", "role": "admin", "avatarUrl": null }
}
```
- ຜິດ email/password → `401` (ຂໍ້ຄວາມບໍ່ບອກວ່າຜິດອັນໃດ)

**POST `/auth/refresh`**: Request `{ "refreshToken": "..." }` → ຕອບ `accessToken` + `refreshToken` ໃໝ່. ຖ້າໝົດອາຍຸ/ຖືກໃຊ້ແລ້ວ → `401`.

> ທາງເລືອກ: ຖ້າຢາກໃຫ້ຜູ້ສະໝັກໃຊ້ httpOnly cookie ແທນ ໃຫ້ແຈ້ງລ່ວງໜ້າ (ຕ້ອງຕັ້ງ `SameSite`/CORS credentials ໃຫ້ຮອງຮັບ localhost).

### 2.2 Employee

**Model `Employee`**
```json
{
  "id": "e_001",
  "employeeCode": "EMP-0001",
  "fullName": "ສົມຊາຍ ວົງສະຫວັນ",
  "email": "somchai@example.com",
  "phone": "020 5555 5555",
  "department": { "id": "d_1", "name": "IT" },
  "position": "Developer",
  "status": "active",
  "hireDate": "2024-03-01",
  "avatarUrl": null,
  "version": 3,
  "createdAt": "...",
  "updatedAt": "..."
}
```
`status`: `active` | `inactive` | `on_leave`

| Method | Path | ລາຍລະອຽດ | ສິດ |
|---|---|---|---|
| GET | `/employees` | ລາຍການ | admin: ທັງໝົດ · employee: ເບິ່ງລາຍຊື່ໄດ້ (ຫຼື ສະເພາະຕົນເອງ, ກຳນົດແລ້ວແຈ້ງ) |
| GET | `/employees/{id}` | ລາຍລະອຽດ | admin ທັງໝົດ · employee ສະເພາະຕົນເອງ |
| POST | `/employees` | ສ້າງ | admin |
| PATCH | `/employees/{id}` | ແກ້ໄຂ | admin ທັງໝົດ · employee ສະເພາະຕົນເອງ (ແກ້ໄດ້ສະເພາະ `fullName`, `phone`, `avatarUrl`; ແກ້ `role`/`status`/`department` ບໍ່ໄດ້) |
| DELETE | `/employees/{id}` | ລຶບ (soft delete ກໍໄດ້) | admin |
| POST | `/employees/{id}/avatar` | ອັບໂຫຼດຮູບ (`multipart/form-data`, ≤ 2MB, jpg/png/webp) | admin / ເຈົ້າຂອງ |
| GET | `/departments` | ລາຍການພະແນກ (ສຳລັບ dropdown/filter) | ທຸກຄົນທີ່ login |

**GET `/employees` query:**
- `page`, `limit`
- `search` (ຄົ້ນຫາ `fullName`, `email`, `employeeCode`; ຮອງຮັບຕົວອັກສອນລາວ)
- `departmentId`, `status` (filter ໄດ້ຫຼາຍຄ່າ ຄັ່ນດ້ວຍ comma)
- `sortBy` (`fullName`, `hireDate`, `createdAt`), `sortOrder` (`asc` | `desc`)

**Validation ທີ່ຕ້ອງມີ (ຕອບ 422 ພ້ອມ `details[].field`):**
- `email`: ຮູບແບບຖືກຕ້ອງ ແລະ **ບໍ່ຊ້ຳ** (`409` ຫຼື `422`)
- `fullName`: ບັງຄັບ, 2–100 ຕົວອັກສອນ
- `phone`: ຮູບແບບເບີໂທ
- `departmentId`: ຕ້ອງມີຢູ່ແທ້
- `hireDate`: ບໍ່ເປັນວັນທີໃນອະນາຄົດ

**POST `/employees`:** ນອກຈາກ field ຂ້າງເທິງ ຕ້ອງຮັບ `password` (ບັງຄັບ, ≥ 8 ຕົວ) ແລະ ສ້າງບັນຊີ login `role = employee` ພ້ອມກັນ ຕາມຂໍ້ 1.6. Employee ຈະຖືກຜູກກັບ Admin ທີ່ສ້າງ (`ownerAdminId`) ແລະ ຈະ chat ກັບ Admin ຄົນນັ້ນ.

---

## 3. A2: Chat (Employee ↔ Admin)

### 3.1 ກົດທຸລະກິດ
- ພະນັກງານ 1 ຄົນມີ **1 ຫ້ອງສົນທະນາ (conversation)** ກັບ **Admin ທີ່ສ້າງພະນັກງານຄົນນັ້ນ** (ຕາມຂໍ້ 1.6). ຫ້ອງຖືກສ້າງອັດຕະໂນມັດຕອນສ້າງພະນັກງານ ເພື່ອໃຫ້ Admin ເຫັນໃນ inbox ທັນທີ ແລະ ເລີ່ມສົນທະນາກ່ອນໄດ້
- ການສົນທະນາມີສະເພາະ **Employee ↔ Admin** (ພະນັກງານ chat ກັນເອງບໍ່ຢູ່ໃນຂອບເຂດ)
- **Employee** ເຂົ້າເຖິງໄດ້ສະເພາະ conversation ຂອງຕົນ. **Admin** ເຂົ້າເຖິງໄດ້ທຸກ conversation ຂອງພະນັກງານທີ່ຕົນສ້າງ (ບໍ່ເຫັນຂອງ Admin ຄົນອື່ນ)
- ເຂົ້າເຖິງຂອງຄົນອື່ນ → `403` (ຕ້ອງບັງຄັບຝັ່ງ server ທັງ REST ແລະ WebSocket)
- ເນື້ອຫາຂໍ້ຄວາມເກັບເປັນ plain text (Frontend ເປັນຜູ້ escape; Backend ຄວນຈຳກັດຄວາມຍາວ ≤ 2,000 ຕົວອັກສອນ)

### 3.2 Models

**Conversation**
```json
{
  "id": "c_001",
  "employee": { "id": "e_001", "fullName": "...", "avatarUrl": null },
  "lastMessage": { "id": "m_099", "text": "ສະບາຍດີ", "senderRole": "employee", "createdAt": "..." },
  "unreadCount": 2,
  "updatedAt": "..."
}
```
`unreadCount` ຄິດຈາກມຸມມອງຂອງຜູ້ທີ່ເອີ້ນ API (Admin ເຫັນທີ່ພະນັກງານສົ່ງ, Employee ເຫັນທີ່ Admin ສົ່ງ)

**Message**
```json
{
  "id": "m_100",
  "clientMessageId": "8f14e45f-...",
  "conversationId": "c_001",
  "sender": { "id": "u_1", "fullName": "...", "role": "admin" },
  "text": "ຮັບຊາບ",
  "status": "sent",
  "createdAt": "2026-10-05T08:30:00Z",
  "readAt": null
}
```
`status`: `sent` | `delivered` | `read`

### 3.3 REST Endpoints

| Method | Path | ລາຍລະອຽດ | ສິດ |
|---|---|---|---|
| GET | `/chat/conversations` | ລາຍການ conversation (`page`, `limit`, `search`, `unreadOnly=true`) ລຽງຕາມ `updatedAt` ໃໝ່ສຸດ | admin: ທັງໝົດ · employee: ຂອງຕົນ (1 ລາຍການ) |
| POST | `/chat/conversations` | ສ້າງ/ດຶງ conversation ຂອງຕົນເອງ (idempotent: ມີແລ້ວກໍສົ່ງອັນເດີມ) | employee |
| GET | `/chat/conversations/{id}` | ລາຍລະອຽດ | ຕາມສິດ |
| GET | `/chat/conversations/{id}/messages` | ປະຫວັດຂໍ້ຄວາມ (cursor pagination) | ຕາມສິດ |
| POST | `/chat/conversations/{id}/messages` | ສົ່ງຂໍ້ຄວາມ (ທາງ REST, ໃຊ້ເປັນ fallback/ຫຼັກ) | ຕາມສິດ |
| POST | `/chat/conversations/{id}/read` | ໝາຍວ່າອ່ານແລ້ວ: `{ "lastReadMessageId": "m_100" }` | ຕາມສິດ |
| GET | `/chat/unread-count` | ຈຳນວນທີ່ຍັງບໍ່ໄດ້ອ່ານລວມ (ສຳລັບ badge) | ທຸກຄົນທີ່ login |
| POST | `/chat/ws-ticket` | ຂໍ ticket ອາຍຸສັ້ນ (30–60 ວິ, ໃຊ້ຄັ້ງດຽວ) ເພື່ອເປີດ WebSocket | ທຸກຄົນທີ່ login |

**GET `.../messages` (cursor):**
- Query: `limit` (ຄ່າເລີ່ມຕົ້ນ 30, ສູງສຸດ 100), `before` (message id → ໂຫຼດຍ້ອນຫຼັງ), `after` (message id → ໂຫຼດຂໍ້ຄວາມທີ່ໃໝ່ກວ່າ, ໃຊ້ຕອນ reconnect ເພື່ອ sync ຂໍ້ຄວາມທີ່ຂາດ)
- Response:
```json
{
  "data": [ /* ລຽງຈາກເກົ່າ → ໃໝ່ */ ],
  "meta": { "hasMoreBefore": true, "hasMoreAfter": false, "nextBefore": "m_070" }
}
```

**POST `.../messages`:**
```json
// Request
{ "clientMessageId": "8f14e45f-...", "text": "ສະບາຍດີຄັບ" }
// Response 201 → Message
```
- **ຕ້ອງ idempotent ດ້ວຍ `clientMessageId`**: ສົ່ງຊ້ຳ ID ດຽວກັນ ຕ້ອງໄດ້ຂໍ້ຄວາມເດີມ (ບໍ່ສ້າງຊ້ຳ). ສຳຄັນສຳລັບ retry ຕອນເນັດຫຼຸດ
- ຂໍ້ຄວາມຫວ່າງ ຫຼື ຍາວເກີນ → `422`

### 3.4 Real-time: WebSocket

> ຖ້າທີມເລືອກ SSE ແທນ ໃຫ້ແຈ້ງຜູ້ຮັບຜິດຊອບ Frontend ລ່ວງໜ້າ ແລະ ໃຊ້ event ຊື່ດຽວກັນ.

- Endpoint: `wss://<host>/ws/chat?ticket=<ticket>` (ຫ້າມໃສ່ access token ໃນ URL; ໃຊ້ ticket ຈາກ `/chat/ws-ticket`)
- ກວດ Origin ແລະ ຢືນຢັນຕົວຕົນຕອນ connect; ຖ້າບໍ່ຜ່ານ ປິດດ້ວຍ code `4401`
- Server ສົ່ງ ping ທຸກ ~25 ວິ (Frontend ຕອບ pong) ເພື່ອກວດ connection ຄ້າງ
- ຮູບແບບ envelope: `{ "type": "<event>", "data": { }, "ts": "..." }`

**Server → Client**

| type | data | ສົ່ງເມື່ອ |
|---|---|---|
| `message.new` | `Message` | ມີຂໍ້ຄວາມໃໝ່ (ສົ່ງໃຫ້ທັງຜູ້ສົ່ງ ແລະ ຜູ້ຮັບ ທຸກ session) |
| `message.status` | `{ messageId, conversationId, status, readAt }` | ສະຖານະປ່ຽນ (`delivered`, `read`) |
| `conversation.updated` | `Conversation` | lastMessage/unreadCount ປ່ຽນ (ໃຫ້ inbox ຂອງ Admin ອັບເດດ) |
| `unread.updated` | `{ total }` | ຈຳນວນລວມທີ່ຍັງບໍ່ອ່ານປ່ຽນ |
| `typing` | `{ conversationId, userId, isTyping }` | ອີກຝ່າຍກຳລັງພິມ |
| `presence` | `{ userId, online, lastSeenAt }` | ສະຖານະ online/offline |
| `error` | `{ code, message }` | ຂໍ້ຜິດພາດ |

**Client → Server**

| type | data | ໝາຍເຫດ |
|---|---|---|
| `typing` | `{ conversationId, isTyping }` | Server relay ໃຫ້ອີກຝ່າຍ (ບໍ່ເກັບ) |
| `message.read` | `{ conversationId, lastReadMessageId }` | ເທົ່າກັບ REST `/read` |
| `ping` | `{}` | ຖ້າຕ້ອງການ |

- ການສົ່ງຂໍ້ຄວາມ: ໃຊ້ REST `POST .../messages` ເປັນຫຼັກ (ງ່າຍຕໍ່ retry/idempotency) ແລ້ວ server broadcast `message.new` ທາງ WebSocket
- ຮອງຮັບຫຼາຍ session ຕໍ່ຜູ້ໃຊ້ (ຫຼາຍແທັບ/ຫຼາຍ browser) ໂດຍທຸກ session ໄດ້ event ຄືກັນ
- ຫຼັງ reconnect Frontend ຈະເອີ້ນ `GET .../messages?after=<lastId>` ເພື່ອ sync, ສະນັ້ນ `after` ຕ້ອງເຮັດວຽກຖືກຕ້ອງ

---

## 4. A3: ຟອມ Dynamic 3 ຊັ້ນ (POST ເສັ້ນດຽວ)

> **ໝາຍເຫດ:** ໂດເມນ "ໂຄງການ → ໄລຍະ → ໜ້າວຽກ" ດ້ານລຸ່ມເປັນ **ຕົວຢ່າງ**. ຖ້າທີມມີໂດເມນອື່ນທີ່ເໝາະກວ່າ (ເຊັ່ນ ໃບສັ່ງຊື້ → ລາຍການ → ຕົວເລືອກ) ປ່ຽນໄດ້ ແຕ່ຕ້ອງຮັກສາ **ໂຄງສ້າງ 3 ຊັ້ນ** ແລະ ຄຸນລັກສະນະຂອງ validation ດ້ານລຸ່ມ.

### 4.1 Endpoints

| Method | Path | ລາຍລະອຽດ | ສິດ |
|---|---|---|---|
| POST | `/projects` | **ສ້າງທັງ 3 ຊັ້ນໃນຄຳຂໍດຽວ** | admin |
| GET | `/projects` | ລາຍການ (`page`, `limit`, `search`) | ທຸກຄົນທີ່ login |
| GET | `/projects/{id}` | ດຶງທັງ 3 ຊັ້ນ (ໃຫ້ຜູ້ສະໝັກກວດວ່າບັນທຶກຖືກ) | ທຸກຄົນທີ່ login |
| DELETE | `/projects/{id}` | ລຶບ (ໃຫ້ຜູ້ທົດສອບລ້າງຂໍ້ມູນ) | admin |
| GET | `/lookups/task-types` | ປະເພດໜ້າວຽກ (dropdown) | ທຸກຄົນທີ່ login |
| GET | `/lookups/priorities` | ລະດັບຄວາມສຳຄັນ | ທຸກຄົນທີ່ login |

ຜູ້ຮັບຜິດຊອບ (`assigneeId`) ໃຊ້ `GET /employees?search=` ຈາກ A1.

### 4.2 Payload ຂອງ `POST /projects`

Header: `Idempotency-Key: <uuid>` (ຮອງຮັບ; ສົ່ງຊ້ຳ key ດຽວກັນຕ້ອງໄດ້ຜົນເດີມ ບໍ່ສ້າງຊ້ຳ)

```json
{
  "name": "ລະບົບ HR ໃໝ່",
  "code": "PRJ-2026-001",
  "description": "ລາຍລະອຽດ...",
  "ownerId": "e_001",
  "startDate": "2026-11-01",
  "endDate": "2027-03-31",
  "phases": [
    {
      "name": "ວິເຄາະຄວາມຕ້ອງການ",
      "order": 1,
      "startDate": "2026-11-01",
      "endDate": "2026-12-15",
      "tasks": [
        {
          "title": "ສຳພາດຜູ້ໃຊ້",
          "type": "feature",
          "priority": "high",
          "assigneeId": "e_002",
          "estimateHours": 16,
          "dueDate": "2026-11-20",
          "severity": null
        },
        {
          "title": "ແກ້ບັນຫາ login",
          "type": "bug",
          "priority": "critical",
          "assigneeId": "e_003",
          "estimateHours": 4,
          "dueDate": "2026-11-10",
          "severity": "major"
        }
      ]
    }
  ]
}
```

**Response `201`** ສົ່ງ object ດຽວກັນກັບຄືນ ພ້ອມ `id` ຂອງທຸກຊັ້ນ (`project.id`, `phases[].id`, `phases[].tasks[].id`), `createdAt`, `version`.

### 4.3 ກົດ Validation (ຕອບ `422`)

| ຊັ້ນ | ກົດ |
|---|---|
| Project | `name` ບັງຄັບ 3–100 ຕົວ; `code` ບໍ່ຊ້ຳ (`409`); `ownerId` ຕ້ອງມີຢູ່; `endDate` ≥ `startDate`; `phases` ມີ ≥ 1 ແລະ ≤ 20 |
| Phase | `name` ບັງຄັບ; `order` ບໍ່ຊ້ຳໃນໂຄງການດຽວກັນ; ວັນທີຢູ່ພາຍໃນຊ່ວງຂອງ Project; `tasks` ມີ ≥ 1 ແລະ ≤ 100 |
| Task | `title` ບັງຄັບ; `type` ຢູ່ໃນ `task-types`; `priority` ຢູ່ໃນ `priorities`; `assigneeId` ຕ້ອງມີຢູ່ ແລະ `status = active`; `estimateHours` > 0 ແລະ ≤ 999; `dueDate` ຢູ່ພາຍໃນຊ່ວງຂອງ Phase |
| **Conditional** | ຖ້າ `task.type = "bug"` → `severity` **ບັງຄັບ** (`minor` / `major` / `critical`); ຖ້າບໍ່ແມ່ນ bug → `severity` ຕ້ອງເປັນ `null` |

- ລວມທຸກ task ໃນຄຳຂໍດຽວ **ຮອງຮັບ ≥ 500 ລາຍການ** (ໃຫ້ຜູ້ສະໝັກທົດສອບ performance)
- **Transaction:** ບັນທຶກທັງ 3 ຊັ້ນ ຫຼື ບໍ່ບັນທຶກເລີຍ (all-or-nothing)

**ຮູບແບບ error ຕ້ອງລະບຸ field path ແບບເຕັມ** ເພື່ອໃຫ້ Frontend ໄປສະແດງຖືກ input:
```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "ຂໍ້ມູນບໍ່ຖືກຕ້ອງ",
    "details": [
      { "field": "name", "message": "ຕ້ອງມີຢ່າງໜ້ອຍ 3 ຕົວອັກສອນ" },
      { "field": "phases[0].endDate", "message": "ຕ້ອງຢູ່ພາຍໃນຊ່ວງຂອງໂຄງການ" },
      { "field": "phases[0].tasks[1].severity", "message": "ບັງຄັບເມື່ອປະເພດເປັນ bug" },
      { "field": "phases[2].tasks[0].assigneeId", "message": "ບໍ່ພົບພະນັກງານ" }
    ]
  }
}
```
> ໃຫ້ຕອບ error ຂອງທຸກ field ໃນຄັ້ງດຽວ (ບໍ່ແມ່ນພຽງ error ທຳອິດ).

---

## 5. ສິ່ງທີ່ຕ້ອງເຕີມ ແລະ ສົ່ງມອບ

### 5.1 ຂໍ້ມູນ Seed
- **Admin** ສ້າງໃນ DB ໂດຍກົງ (ຕາມຂໍ້ 1.6) ແຕ່ລະບັນຊີມີ `email`, `password` ທີ່ hash ແລ້ວ ແລະ `role = admin`
- **ຜູ້ສະໝັກສ້າງພະນັກງານເອງ** ຈຶ່ງບໍ່ຈຳເປັນຕ້ອງ seed ພະນັກງານ. ແຕ່ຖ້າຕ້ອງການໃຫ້ທົດສອບ pagination/search ໂດຍບໍ່ຕ້ອງສ້າງດ້ວຍມື ໃຫ້ seed ພະນັກງານຕົວຢ່າງ 30–50 ຄົນ (ບໍ່ມີບັນຊີ login) ຕໍ່ Admin, ມີຊື່ລາວ/ອັງກິດປົນກັນ
- ພະແນກ ≥ 5, `task-types`, `priorities` ຄົບ (ໃຊ້ຮ່ວມກັນ, ບໍ່ຂຶ້ນກັບ Admin)
- ຕາຕະລາງບັນຊີທົດສອບ (ເຕີມກ່ອນສົ່ງ, ຈະຖືກແນບໄປໃນເອກະສານຂອງຜູ້ສະໝັກ):

| ຜູ້ສະໝັກ | Admin email | Password | ໝົດອາຍຸ |
|---|---|---|---|
| candidate01 | | | |
| candidate02 | | | |

### 5.2 ເອກະສານ ແລະ ສະພາບແວດລ້ອມ
- ☐ Swagger/OpenAPI (`/docs` ແລະ `openapi.json`) ຫຼື Postman collection ພ້ອມຕົວຢ່າງ request/response ຄົບ
- ☐ ເອກະສານ WebSocket (event ທັງໝົດ ພ້ອມຕົວຢ່າງ)
- ☐ ເຊີບເວີທີ່ເຂົ້າເຖິງໄດ້ຈາກອິນເຕີເນັດ (HTTPS/WSS) ແລະ ຕັ້ງ CORS ຖືກຕ້ອງ
- ☐ ບັນຊີ Admin ສ້າງໃນ DB ແລ້ວ, ທົດລອງ login ໄດ້, ແລະ ທົດສອບ flow ເຕັມ: Admin ສ້າງພະນັກງານ → ພະນັກງານ login → chat ກັບ Admin ໄດ້
- ☐ ທົດສອບການແຍກຂໍ້ມູນ: Admin ສອງບັນຊີບໍ່ເຫັນຂໍ້ມູນຂອງກັນ
- ☐ URL ແລະ ບັນຊີ Admin ພ້ອມແນບໃນເອກະສານຂອງຜູ້ສະໝັກ
- ☐ ວິທີ reset ຂໍ້ມູນທົດສອບ (script ຫຼື endpoint ພາຍໃນ) ເພື່ອລ້າງຂໍ້ມູນຫຼັງຜູ້ສະໝັກແຕ່ລະຄົນ

### 5.3 ລຳດັບການສົ່ງມອບທີ່ແນະນຳ
1. **Auth + Employee (A1)** ພ້ອມ Swagger
2. **A3** (`/projects` + lookups)
3. **Chat REST**, ແລ້ວ **WebSocket** (A2)
4. Seed data ແລະ ເອກະສານສຸດທ້າຍ

### 5.4 ບໍ່ບັງຄັບ (ຄະແນນເພີ່ມ)
- ແນບໄຟລ໌/ຮູບໃນ chat (`POST /chat/attachments`, ≤ 5MB)
- ETag / `If-None-Match` ໃນ `GET /employees/{id}`
- ໃສ່ຄວາມຊ້າ ຫຼື error ແບບສຸ່ມ ໂດຍເປີດ/ປິດດ້ວຍ header ພິເສດ (ເຊັ່ນ `X-Simulate-Delay: 2000`) ເພື່ອທົດສອບຄວາມທົນທານຂອງ Frontend

---

## 6. ຄຳຖາມທີ່ຕ້ອງຕອບກັບ Frontend ກ່ອນເລີ່ມ (ຜູ້ປະສານງານ)
ຕົກລົງແລ້ວ: Admin ສ້າງໃນ DB, Chat ເປັນ Employee ↔ Admin ທີ່ສ້າງພະນັກງານນັ້ນ, ແລະ ຜູ້ສະໝັກສ້າງພະນັກງານເອງ. ຍັງຕ້ອງຢືນຢັນ:
1. Chat ໃຊ້ WebSocket ຫຼື SSE?
2. Employee ເຫັນລາຍຊື່ພະນັກງານທັງໝົດໄດ້ບໍ ຫຼື ສະເພາະຕົນເອງ?
3. ໂດເມນຂອງ A3 ໃຊ້ "ໂຄງການ → ໄລຍະ → ໜ້າວຽກ" ຫຼື ປ່ຽນ?
4. ຈຳນວນຜູ້ສະໝັກທີ່ທົດສອບພ້ອມກັນ (ເພື່ອກຳນົດຈຳນວນ Admin ທີ່ຕ້ອງສ້າງ)?
5. ວັນທີ່ພ້ອມສົ່ງ API ໃຫ້ຜູ້ສະໝັກ: ____ / ____ / ________
