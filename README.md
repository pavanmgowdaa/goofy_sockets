# FAIRPASS backend

FAIRPASS is a Go REST API backed by a global PostgreSQL database. The 360° ticketing page is embedded in the API and served at `/`; it reads published events and ticket inventory from the API, authenticates attendees, reserves tickets, and uses Razorpay Checkout. Gate hardware and its local offline manifest/scan store remain a separate system; this service does not modify `../database/local/schema.sql` or perform physical gate control.

## Run locally

1. Copy `.env.example` to `.env` and set `DATABASE_URL` and a unique random `JWT_SECRET` of at least 32 characters.
2. For a new local database, `docker compose up --build` initializes all migrations. For an existing database, review and apply unapplied migrations in order (`000002` through `000008`) with the normal migration tool. They are additive; this project does not run migrations at API startup.
3. Run `go run ./cmd/api` or use Compose. `GET /healthz` checks PostgreSQL.

For WhatsApp-only sign-in, set `TWILIO_ACCOUNT_SID`, `TWILIO_AUTH_TOKEN`, and
`TWILIO_VERIFY_SERVICE_SID` for a Twilio Verify service with WhatsApp enabled.
Every user, including administrators, signs in with a phone number in E.164
format and a WhatsApp verification code. Existing accounts need their verified
phone number populated in `users.phone_e164` before they can sign in. For a
pre-existing account, set `FAIRPASS_LINK_EMAIL` and `FAIRPASS_LINK_PHONE` and
run `go run ./cmd/account-link-phone`; it links only an account with no phone
already assigned and refuses to overwrite one. For a
fresh database, bootstrap the first administrator once before organizer
verification: set `FAIRPASS_ADMIN_NAME` and `FAIRPASS_ADMIN_PHONE`, then run
`go run ./cmd/admin-bootstrap`. It creates an administrator only when none
exists and never promotes or edits an existing account. Afterward, attendees
can register with WhatsApp OTP, an administrator can verify organizers through
`POST /v1/admin/users/{id}/verify-organizer`, and the verified organizer can
create a draft event, add ticket types, and publish it.

To make the empty development catalog purchasable, run `go run ./cmd/demo-seed`
after the first administrator exists. It inserts one published sample event
only when there are no published events; otherwise it leaves the database
untouched. Configure Razorpay credentials and a reachable webhook before
expecting checkout to complete.

Do not commit `.env`. Keep `QUAI_PRIVATE_KEY` only in the `quai-bridge` service. The Go API receives only the bridge URL/token and contract address. Use separate keys for each environment and fund the Quai signer with only the required testnet/production balance.

## Current API capabilities

- Global account registration/login via WhatsApp OTP, JWT access and rotating refresh tokens, logout, role checks, and database-backed per-IP/route rate limiting.
- Organizer event drafts, ticket inventory bounded by event capacity, admin organizer verification, publishing only by verified organizers, published event/ticket listings, inventory reservations, per-user purchase limits, idempotent bookings, and reservation expiry.
- Razorpay order creation, HMAC-verified webhook processing with event deduplication, capture/amount checks, ticket issuance, payment-attempt tracking, and retried idempotent refunds.
- Paid fan-to-fan resale listings with event price caps and Razorpay checkout. A verified capture atomically transfers ownership and rotates/re-signs the QR; resale buyer payment currently settles to the platform account, so seller payout integration remains outstanding.
- High-entropy QR credentials with SHA-256 digests in the ticket table (passwords use bcrypt; SHA-256 is appropriate here because the QR secrets are random and high entropy). Ticket listing and owner-only QR retrieval, recipient transfers with QR rotation, gate scan replay protection, and audit records.
- New tickets receive a deterministic, domain-separated SHA-256 hash over canonical ticket identity data. If `TICKET_SIGNING_PRIVATE_KEY` is configured, the backend also signs that canonical data with Ed25519 and gate verification checks the signature. Existing tickets remain legacy version 0 and keep their existing identifiers and QR hashes.
- Quai Solidity registry, Quais SDK bridge, address validation, registration/transfer/redemption jobs, transaction receipt/confirmation polling, and retry tracking. Chain operations are asynchronous and require a deployed contract plus bridge/RPC credentials.
- Device-authenticated scan, heartbeat, and sensor-ingestion endpoints remain available when `DEVICE_API_KEY_PEPPER` is configured. These endpoints report gate decisions; they do not drive an actual servo or other hardware.

Abuse controls include IP/route rate limits, Twilio Verify OTP limits, account purchase caps, idempotency, strict request decoding, and optional Cloudflare Turnstile verification on ticket and resale checkout. Set both `TURNSTILE_SITE_KEY` and `TURNSTILE_SECRET_KEY` to enable the visible challenge. Without them, rate limits remain active. These controls reduce abuse but cannot guarantee protection from distributed automated traffic; put the service behind TLS and a correctly configured trusted proxy.

## Quai setup

`quai-bridge/deploy.mjs --compile-only` compiles the contract. To deploy, set `QUAI_RPC_URL` and `QUAI_PRIVATE_KEY` in the bridge/deployment environment and run `node quai-bridge/deploy.mjs`; copy the emitted address into `QUAI_CONTRACT_ADDRESS`. Use the matching chain endpoint and configuration from the [official Quai docs](https://docs.qu.ai/sdk/static/provider). For local compose, set a bridge bearer token of at least 32 random characters. Leaving Quai credentials empty disables chain jobs while online booking remains usable.

## Remaining production work

The project still needs automated seller settlement through Razorpay Route or an equivalent connected-account integration, SMTP outbox delivery, venue/admin provisioning APIs, event update/cancellation workflows, and database-backed integration tests against migrations and real PostgreSQL. Hardware integration remains explicitly postponed. OpenAPI is in `docs/openapi.yaml` and should be kept aligned as routes evolve.

When the API is running, open `http://localhost:8080/` (not the standalone `file://` copy) to use live events. Set all three Razorpay credentials in `.env`, expose `/v1/payments/razorpay/webhook` through a public HTTPS URL in the Razorpay dashboard, and configure that webhook secret. In production, configure a stable base64-encoded 64-byte Ed25519 private key as `TICKET_SIGNING_PRIVATE_KEY`; the API refuses production startup without it. The UI treats the webhook-confirmed booking status as authoritative; the browser payment callback alone never issues tickets.
