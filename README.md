# Chirpy

Chirpy is a Go HTTP API for short posts ("chirps") with JWT auth, refresh-token auth flow, user management, and a webhook-based premium upgrade feature.

## Overview

- Language: Go
- HTTP server: `net/http`
- Database: PostgreSQL
- Query layer: `sqlc`-generated code under `internal/database/`
- Auth:
  - JWT access tokens
  - Refresh tokens stored in PostgreSQL
  - API key auth for Polka webhook endpoint

## Prerequisites

- Go 1.22+ (or newer)
- PostgreSQL 14+ (or newer)
- `psql` CLI (recommended for schema setup)
- Git

## Installation and Local Setup

1. Clone the project:

```bash
git clone https://github.com/pipastalk/Chirpy
cd Chirpy
```

2. Install Go dependencies:

```bash
go mod download
```

3. Create a PostgreSQL database (example):

```bash
createdb chirpy
```

4. Apply schema files in order:

```bash
psql "$DB_URL" -f sql/schema/001_users.sql
psql "$DB_URL" -f sql/schema/002_posts.sql
psql "$DB_URL" -f sql/schema/003_users.sql
psql "$DB_URL" -f sql/schema/004_refresh_tokens.sql
psql "$DB_URL" -f sql/schema/005_users.sql
```

5. Create a `.env` file in the project root:

```env
DB_URL=postgres://<username>:<password>@localhost:5432/chirpy?sslmode=disable
jwt_secret=<a-strong-random-secret>
POLKA_KEY=<your-polka-webhook-api-key>
```

6. Run the app:

```bash
go run .
```

Server default address: `http://localhost:8080`

## API Routes

Base URL: `http://localhost:8080`

### Route Summary (from API routes region)

| Method | Path | Auth | Notes |
|---|---|---|---|
| GET | `/app/*` | None | Serves static files from project root |
| GET | `/api/healthz` | None | Health check |
| POST | `/api/chirps` | Bearer JWT | Create chirp |
| GET | `/api/chirps` | None | List chirps (supports query params) |
| GET | `/api/chirps/{chirpID}` | None | Get one chirp |
| DELETE | `/api/chirps/{chirpID}` | Bearer JWT | Delete own chirp |
| POST | `/api/users` | None | Register user |
| GET | `/api/users` | None | Look up user by email (expects JSON body) |
| PUT | `/api/users` | Bearer JWT | Update current user |
| POST | `/api/login` | None | Login with email/password |
| POST | `/api/refresh` | Bearer Refresh Token | Exchange refresh token for new access token |
| POST | `/api/revoke` | Bearer Refresh Token | Revoke refresh token |
| POST | `/api/polka/webhooks` | API Key | Upgrade user to Chirpy Red on webhook |
| GET | `/admin/metrics` | None | Admin HTML metrics page |
| POST | `/admin/reset` | None | Reset users + file server hit count |

### Endpoint Details

#### 0) Static App Files

`GET /app/*`

Serves static files through Go's `http.FileServer` (strip prefix `/app`).

Example:

- `GET /app/` can serve `index.html`

---

#### 1) Health Check

`GET /api/healthz`

Response: `200 OK` (plain text)

```text
OK
```

---

#### 2) Register User

`POST /api/users`

Request JSON:

```json
{
  "email": "user@example.com",
  "password": "supersecret"
}
```

Response: `201 Created`

```json
{
  "id": "uuid",
  "email": "user@example.com",
  "created_at": "2026-04-27T12:00:00Z",
  "updated_at": "2026-04-27T12:00:00Z",
  "token": "",
  "refresh_token": "",
  "is_chirpy_red": false
}
```

---

#### 3) Get User by Email

`GET /api/users`

Note: This handler expects a JSON body, even for GET.

Request JSON:

```json
{
  "email": "user@example.com",
  "password": "ignored-for-lookup"
}
```

Response: `200 OK`

```json
{
  "id": "uuid",
  "email": "user@example.com",
  "created_at": "2026-04-27T12:00:00Z",
  "updated_at": "2026-04-27T12:00:00Z",
  "token": "",
  "refresh_token": "",
  "is_chirpy_red": false
}
```

---

#### 4) Login

`POST /api/login`

Request JSON:

```json
{
  "email": "user@example.com",
  "password": "supersecret",
  "expires_in_seconds": 3600
}
```

`expires_in_seconds` is optional. If omitted or invalid, access token defaults to 1 hour.

Response: `200 OK`

```json
{
  "id": "uuid",
  "email": "user@example.com",
  "created_at": "2026-04-27T12:00:00Z",
  "updated_at": "2026-04-27T12:00:00Z",
  "token": "<jwt-access-token>",
  "refresh_token": "<refresh-token>",
  "is_chirpy_red": false
}
```

---

#### 5) Update Current User

`PUT /api/users`

Headers:

```http
Authorization: Bearer <jwt-access-token>
Content-Type: application/json
```

Request JSON:

```json
{
  "email": "new@example.com",
  "password": "newpassword"
}
```

Response: `200 OK`

```json
{
  "id": "uuid",
  "email": "new@example.com",
  "created_at": "2026-04-27T12:00:00Z",
  "updated_at": "2026-04-27T12:30:00Z",
  "token": "",
  "refresh_token": "",
  "is_chirpy_red": false
}
```

---

#### 6) Create Chirp

`POST /api/chirps`

Headers:

```http
Authorization: Bearer <jwt-access-token>
Content-Type: application/json
```

Request JSON:

```json
{
  "body": "hello chirpy"
}
```

Rules:

- Body must not be empty
- Body max length is 140 characters
- Profane words are sanitized

Response: `201 Created`

```json
{
  "id": "uuid",
  "body": "hello chirpy",
  "created_at": "2026-04-27T12:00:00Z",
  "updated_at": "2026-04-27T12:00:00Z",
  "user_id": "uuid"
}
```

---

#### 7) List Chirps

`GET /api/chirps`

Optional query parameters:

- `author_id=<uuid>`: filter by author
- `sort=asc|desc`: sort by `created_at`

Examples:

- `/api/chirps`
- `/api/chirps?author_id=<uuid>`
- `/api/chirps?sort=desc`
- `/api/chirps?author_id=<uuid>&sort=asc`

Response: `200 OK`

```json
[
  {
    "id": "uuid",
    "body": "hello",
    "created_at": "2026-04-27T12:00:00Z",
    "updated_at": "2026-04-27T12:00:00Z",
    "user_id": "uuid"
  }
]
```

---

#### 8) Get Chirp by ID

`GET /api/chirps/{chirpID}`

Response: `200 OK`

```json
[
  {
    "id": "uuid",
    "body": "single chirp",
    "created_at": "2026-04-27T12:00:00Z",
    "updated_at": "2026-04-27T12:00:00Z",
    "user_id": "uuid"
  }
]
```

Note: Current implementation returns an array with one item.

---

#### 9) Delete Chirp

`DELETE /api/chirps/{chirpID}`

Headers:

```http
Authorization: Bearer <jwt-access-token>
```

Behavior:

- Only the chirp owner can delete the chirp

Response: `204 No Content`

---

#### 10) Refresh Access Token

`POST /api/refresh`

Headers:

```http
Authorization: Bearer <refresh-token>
```

Response: `200 OK`

```json
{
  "token": "<new-jwt-access-token>"
}
```

---

#### 11) Revoke Refresh Token

`POST /api/revoke`

Headers:

```http
Authorization: Bearer <refresh-token>
```

Response: `204 No Content`

---

#### 12) Polka Webhook (Enable Chirpy Red)

`POST /api/polka/webhooks`

Headers:

```http
Authorization: ApiKey <POLKA_KEY>
Content-Type: application/json
```

Request JSON:

```json
{
  "event": "user.upgraded",
  "data": {
    "user_id": "uuid"
  }
}
```

Behavior:

- If `event` is not `user.upgraded`, returns `204 No Content`
- On valid request, updates user to Chirpy Red

Response: `204 No Content`

---

#### 13) Admin Metrics

`GET /admin/metrics`

Response: `200 OK` (HTML page showing file server hit count)

---

#### 14) Admin Reset

`POST /admin/reset`

Behavior:

- Resets user DB
- Resets file server hit counter

Response: `200 OK` with text:

```text
Hits reset
```

## Authentication Reference

### Bearer JWT

Used by protected user/chirp endpoints.

```http
Authorization: Bearer <jwt-access-token>
```

### Bearer Refresh Token

Used by token refresh/revoke endpoints.

```http
Authorization: Bearer <refresh-token>
```

### Polka API Key

Used by webhook endpoint.

```http
Authorization: ApiKey <POLKA_KEY>
```

## cURL Reference (All Routes)

Use these commands as a quick copy/paste reference for every route in the API routes region.

1. Static app files

```bash
curl http://localhost:8080/app/
```

2. Health check

```bash
curl http://localhost:8080/api/healthz
```

3. Register user

```bash
curl -X POST http://localhost:8080/api/users \
  -H "Content-Type: application/json" \
  -d '{"email":"user@example.com","password":"supersecret"}'
```

4. Get user by email (GET with body)

```bash
curl -X GET http://localhost:8080/api/users \
  -H "Content-Type: application/json" \
  -d '{"email":"user@example.com","password":"placeholder"}'
```

5. Login

```bash
curl -X POST http://localhost:8080/api/login \
  -H "Content-Type: application/json" \
  -d '{"email":"user@example.com","password":"supersecret","expires_in_seconds":3600}'
```

6. Update current user

```bash
curl -X PUT http://localhost:8080/api/users \
  -H "Authorization: Bearer <jwt-access-token>" \
  -H "Content-Type: application/json" \
  -d '{"email":"new@example.com","password":"newpassword"}'
```

7. Create chirp

```bash
curl -X POST http://localhost:8080/api/chirps \
  -H "Authorization: Bearer <jwt-access-token>" \
  -H "Content-Type: application/json" \
  -d '{"body":"my first chirp"}'
```

8. List chirps

```bash
curl http://localhost:8080/api/chirps
```

Optional filtered/sorted list example:

```bash
curl "http://localhost:8080/api/chirps?author_id=<user-uuid>&sort=desc"
```

9. Get chirp by ID

```bash
curl http://localhost:8080/api/chirps/<chirp-uuid>
```

10. Delete chirp by ID

```bash
curl -X DELETE http://localhost:8080/api/chirps/<chirp-uuid> \
  -H "Authorization: Bearer <jwt-access-token>"
```

11. Refresh access token

```bash
curl -X POST http://localhost:8080/api/refresh \
  -H "Authorization: Bearer <refresh-token>"
```

12. Revoke refresh token

```bash
curl -X POST http://localhost:8080/api/revoke \
  -H "Authorization: Bearer <refresh-token>"
```

13. Polka webhook

```bash
curl -X POST http://localhost:8080/api/polka/webhooks \
  -H "Authorization: ApiKey <POLKA_KEY>" \
  -H "Content-Type: application/json" \
  -d '{"event":"user.upgraded","data":{"user_id":"<user-uuid>"}}'
```

14. Admin metrics

```bash
curl http://localhost:8080/admin/metrics
```

15. Admin reset

```bash
curl -X POST http://localhost:8080/admin/reset
```

## Troubleshooting

### Missing Environment Variables

If the server exits on startup, verify your `.env` file exists in the project root and includes all required keys:

- `DB_URL`
- `jwt_secret`
- `POLKA_KEY`

Quick checks:

```bash
cat .env
```

```bash
grep -E '^(DB_URL|jwt_secret|POLKA_KEY)=' .env
```

If one is missing, add it and restart:

```bash
go run .
```

### Token and Auth Mistakes

If you get `401 Unauthorized`, it is usually one of these:

- Missing `Authorization` header
- Wrong token type for endpoint
  - Access token (JWT) is required for protected user/chirp routes
  - Refresh token is required for `/api/refresh` and `/api/revoke`
- Expired or revoked refresh token
- Invalid API key format/value for webhook route

Use the correct header formats:

```http
Authorization: Bearer <jwt-access-token>
```

```http
Authorization: Bearer <refresh-token>
```

```http
Authorization: ApiKey <POLKA_KEY>
```

Common fixes:

- Log in again at `/api/login` to get fresh tokens
- Do not send refresh tokens to JWT-protected routes
- Do not send JWT access tokens to `/api/refresh` or `/api/revoke`
- Ensure webhook calls use `ApiKey` (not `Bearer`) and the exact `POLKA_KEY` value
