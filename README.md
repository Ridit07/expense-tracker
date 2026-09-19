# Expense Tracker — Service

Single Go service for the SMS-driven expense tracker. Layered like
[hiremind-ai](https://github.com/Ridit07/hiremind-ai) (config → db → model →
services → transport) but with an **HTTP** transport instead of gRPC, and no
separate gateway — everything lives in one service.

## Pipeline (target)

```
Bank SMS → Ingestion (THIS STEP) → Detection → Sender ID → Validation →
Extraction → Duplicate detection → AI categorization → Confirmation →
Transaction DB → Analytics & budgets
```

Step 1 (this commit) is **ingestion**: receive raw SMS from the client and store
them as `raw_messages` in the `RECEIVED` state. Everything downstream reads from
this table, so the raw text is always kept verbatim for re-processing.

## Layout

```
main.go                  bootstrap: config, db, migrate, http server, graceful shutdown
config/                  env-based configuration
db/                      GORM read/write connection pools
model/                   RawMessage + transaction lifecycle enums
services/                MessageService — ingestion logic + idempotency
transport_http/          router, middleware (auth/log/recover), handlers
common/                  JSON response envelope helpers
errors/                  domain sentinel errors
```

## Running locally

```bash
cp .env.example .env          # set API_KEY etc.
make docker-up                # starts Postgres (needs Docker)
make run                      # or: go run .
```

Without Docker, point `DB_READ_URL` / `DB_WRITE_URL` at any Postgres instance.
The schema is auto-migrated on startup.

## Auth

JWT-based. Register or log in to get a token, then send it as
`Authorization: Bearer <token>` on every `/api/v1/*` request. The user id is
read from the token, so no `X-User-ID` header is needed. Tokens are HS256-signed
with `JWT_SECRET` and valid for 30 days. Passwords are bcrypt-hashed.

### `POST /api/v1/auth/register`

```bash
curl -X POST http://localhost:8080/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"email":"me@example.com","password":"supersecret","name":"Me"}'
```

### `POST /api/v1/auth/login`

```bash
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"me@example.com","password":"supersecret"}'
```

Both return:

```json
{ "success": true, "data": { "token": "eyJ…", "user": { "id": "…", "email": "…" } } }
```

## API

All `/api/v1/*` routes (except `auth`) require the header:

- `Authorization: Bearer <token>`

### `POST /api/v1/messages` — ingest SMS

Single message:

```bash
curl -X POST http://localhost:8080/api/v1/messages \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "sender": "AD-HDFCBK",
    "body": "HDFC Bank: Rs. 1,250.00 debited from A/c XX1234 towards ZOMATO on 19-09-26.",
    "source": "ios_shortcut",
    "received_at": "2026-09-19T10:30:00Z"
  }'
```

Batch (iOS Shortcut can queue several):

```json
{ "messages": [ { "sender": "...", "body": "..." }, { "body": "..." } ] }
```

Response:

```json
{
  "success": true,
  "data": {
    "ingested": 1,
    "duplicate": 0,
    "results": [
      { "id": "…uuid…", "status": "RECEIVED", "duplicate": false }
    ]
  }
}
```

Ingestion is **idempotent**: the same message (user + sender + body +
received_at, truncated to the second) is de-duplicated via a `content_hash`
unique index, so Shortcut retries won't create duplicate rows. This is
raw-level dedup; semantic duplicate detection (same real transaction across
different SMS) is a later pipeline stage.

`received_at` is optional (RFC3339); if omitted the server timestamps it.
`source` defaults to `ios_shortcut`.

### `GET /api/v1/messages?status=RECEIVED&limit=50`

List a user's raw messages, newest first.

### `GET /api/v1/messages/{id}`

Fetch one raw message.

### `GET /health`

Unauthenticated health check.

## iOS ingestion note

iOS does not give apps free access to the SMS inbox. The intended path is an
**Automation in the Shortcuts app**: "When I get a message from [bank senders]"
→ *Get Contents of URL* → POST the message text to `POST /api/v1/messages` with
the `Authorization: Bearer` header. The server is designed around what Shortcuts
permits rather than assuming continuous inbox reads.

## Next steps

`RawMessage.Status` already models the full lifecycle
(`RECEIVED → PARSED → VALIDATED → CATEGORIZED → CONFIRMED`, plus `REJECTED`,
`DUPLICATE`, `REVERSED`, `REFUNDED`, `NEEDS_REVIEW`). The next stage is the
sender identification + transaction eligibility engine that moves messages out
of `RECEIVED`.
