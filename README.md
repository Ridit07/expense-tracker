# Expense Tracker — Service

Single Go service for the SMS-driven expense tracker. Layered like
[hiremind-ai](https://github.com/Ridit07/hiremind-ai) (config → db → model →
services → transport) but with an **HTTP** transport instead of gRPC, and no
separate gateway — everything lives in one service.

## Pipeline (target)

```
Bank SMS → Ingestion (DONE) → Detection (DONE) → Sender ID → Validation →
Extraction → Duplicate detection → AI categorization → Confirmation →
Transaction DB → Analytics & budgets
```

Steps 1–2 are implemented: **ingestion** receives raw SMS from the client and
stores them as `raw_messages`, and **detection** immediately classifies each one
— clear non-transactions (OTPs, marketing, delivery updates, balance enquiries)
land in `REJECTED`, everything else stays in `RECEIVED` for the parser.
Everything downstream reads from this table, so the raw text is always kept
verbatim: rejection only moves the status, so re-running a better detector over
the `REJECTED` rows can always bring them back.

## Layout

```
main.go                  bootstrap: config, db, migrate, http server, graceful shutdown
config/                  env-based configuration
db/                      GORM read/write connection pools
model/                   RawMessage + transaction lifecycle enums
services/                MessageService — ingestion, dedup, sync watermark
                         TransactionDetector — first-pass non-transaction filter
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

Batch (the iOS Shortcut queues a few; the macOS sync tool sends hundreds):

```json
{ "messages": [ { "sender": "...", "body": "..." }, { "body": "..." } ] }
```

Response:

```json
{
  "success": true,
  "data": {
    "ingested": 2,
    "duplicate": 0,
    "rejected": 1,
    "results": [
      { "id": "…uuid…", "status": "RECEIVED", "duplicate": false },
      { "id": "…uuid…", "status": "REJECTED", "duplicate": false, "external_id": "…guid…" }
    ]
  }
}
```

`rejected` counts messages that were stored but classified as non-transactions;
they are ingested, not discarded.

#### Fields

| field | required | notes |
| --- | --- | --- |
| `body` | yes | stored verbatim |
| `sender` | no | SMS sender id, e.g. `AD-HDFCBK` |
| `source` | no | defaults to `ios_shortcut`; must be one of `ios_shortcut`, `android`, `manual`, `api`, `macos_chatdb` — anything else is a `400 invalid_input` |
| `received_at` | no | RFC3339; the server timestamps it if omitted |
| `external_id` | no | the source's own stable id for the message (chat.db's `message.guid`) |

#### Idempotency

Ingestion is **idempotent**, on whichever key the client can offer:

- **`external_id` present** → dedup on `(user_id, external_id)`. This is the
  better key: it survives re-reads of the same `chat.db` and any change to how
  we normalise the body.
- **`external_id` absent** → dedup on `content_hash`, a sha256 over
  (user + sender + body + received_at truncated to the second). Existing
  clients are unaffected.

The two unique indexes are partial and cover disjoint sets of rows, so a batch
mixing both kinds can never abort on a collision with the index it isn't using.
This is raw-level dedup; semantic duplicate detection (same real transaction
across different SMS) is a later pipeline stage.

#### Limits

- Request body: **5 MB**. Over that is `413 payload_too_large` — the body is
  never silently truncated.
- Batch size: **500 messages**. Over that is `400 batch_too_large`.

A backfill should chunk on whichever limit it hits first.

### `GET /api/v1/messages/sync-state?source=macos_chatdb`

The watermark for a pull-based sync tool: what we last received from this user
on this source. It lets the tool be deleted and rebuilt from scratch without
re-sending months of history.

```bash
curl "http://localhost:8080/api/v1/messages/sync-state?source=macos_chatdb" \
  -H "Authorization: Bearer $TOKEN"
```

```json
{
  "success": true,
  "data": {
    "source": "macos_chatdb",
    "last_received_at": "2026-09-30T18:42:11Z",
    "last_external_id": "p:0/8A1C…-GUID",
    "count": 4821
  }
}
```

`source` is optional and validated against the same set as ingest; omit it to
get the totals across every source. A user with nothing stored gets
`{"last_received_at": null, "last_external_id": "", "count": 0}` rather than a
404 — "I have none of your messages" is a valid answer to "where did we leave
off".

### `GET /api/v1/messages?status=RECEIVED&limit=50`

List a user's raw messages, newest first.

### `GET /api/v1/messages/{id}`

Fetch one raw message.

### `GET /health`

Unauthenticated health check.

## macOS ingestion (chat.db)

A companion macOS tool reads `~/Library/Messages/chat.db` directly, taking only
green-bubble SMS (`message.service = 'SMS'`), and POSTs them to
`POST /api/v1/messages` with `source: "macos_chatdb"` and `external_id` set to
`message.guid`. It is the only path that can backfill history — Shortcuts only
ever sees messages as they arrive.

Two loads, one endpoint:

- **Backfill** — months of history, chunked to 500 messages per request. Dedup
  is on `external_id`, so a re-run costs nothing but the round-trips.
- **Incremental sync** — every few minutes. Call `GET /messages/sync-state`
  first and send only what arrived after `last_received_at`.

## iOS ingestion note

iOS does not give apps free access to the SMS inbox. The intended path is an
**Automation in the Shortcuts app**: "When I get a message from [bank senders]"
→ *Get Contents of URL* → POST the message text to `POST /api/v1/messages` with
the `Authorization: Bearer` header. The server is designed around what Shortcuts
permits rather than assuming continuous inbox reads.

## Detection

`services.TransactionDetector` runs inline during ingest and is deliberately
conservative: it only rejects what it recognises as a non-transaction, and
anything it isn't sure about stays `RECEIVED`. A false reject hides real
spending; a false keep only costs the parser a wasted pass.

| reason | example |
| --- | --- |
| `otp` | "123456 is your OTP. Do not share it." |
| `promotional` | "You are pre-approved for a loan of Rs 5,00,000. Apply now!" |
| `delivery_update` | "Your order is out for delivery." |
| `balance_enquiry` | "Avl Bal in A/c XX1234 is Rs. 24,510.22" |

A money verb next to a money amount outranks the promotional and balance rules,
because most real debit SMS carry an "Avl Bal" suffix and many carry marketing
copy. OTP is the one rule that outranks the money signal: an OTP quoting a
transaction amount is not the record of that transaction, and treating it as one
would double-count every online payment.

## Next steps

`RawMessage.Status` already models the full lifecycle
(`RECEIVED → PARSED → VALIDATED → CATEGORIZED → CONFIRMED`, plus `REJECTED`,
`DUPLICATE`, `REVERSED`, `REFUNDED`, `NEEDS_REVIEW`). Detection is in place; the
next stage is the sender identification + transaction eligibility engine that
moves messages out of `RECEIVED`.
