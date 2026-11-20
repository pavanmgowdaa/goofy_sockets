# FAIRPASS backend

FAIRPASS is a Go REST API backed by a global PostgreSQL database. Gate hardware and its local offline manifest/scan store remain a separate system; this service does not modify `../database/local/schema.sql` or perform physical gate control.

## Run locally

1. Copy `.env.example` to `.env` and set `DATABASE_URL` and a unique random `JWT_SECRET` of at least 32 characters.
2. For a new local database, `docker compose up --build` initializes all migrations. For an existing database, review and apply unapplied migrations in order (`000002` through `000005`) with the normal migration tool. They are additive; this project does not run migrations at API startup and none were run during development.
3. Run `go run ./cmd/api` or use Compose. `GET /healthz` checks PostgreSQL.

Do not commit `.env`. Keep `QUAI_PRIVATE_KEY` only in the `quai-bridge` service. The Go API receives only the bridge URL/token and contract address. Use separate keys for each environment and fund the Quai signer with only the required testnet/production balance.

## Current API capabilities

- Global account registration/login, JWT access and rotating refresh tokens, logout, bcrypt password hashes, role checks, database-backed per-IP/route rate limiting, and per-account login lockout.
- Organizer event drafts, ticket inventory bounded by event capacity, admin organizer verification, publishing only by verified organizers, published event/ticket listings, inventory reservations, per-user purchase limits, idempotent bookings, and reservation expiry.
- Razorpay order creation, HMAC-verified webhook processing with event deduplication, capture/amount checks, ticket issuance, payment-attempt tracking, and retried idempotent refunds.
- High-entropy QR credentials with SHA-256 digests in the ticket table (passwords use bcrypt; SHA-256 is appropriate here because the QR secrets are random and high entropy). Ticket listing, recipient transfers, QR rotation on transfer, gate scan replay protection, and audit records.
- Quai Solidity registry, Quais SDK bridge, address validation, registration/transfer/redemption jobs, transaction receipt/confirmation polling, and retry tracking. Chain operations are asynchronous and require a deployed contract plus bridge/RPC credentials.
- Device-authenticated scan, heartbeat, and sensor-ingestion endpoints remain available when `DEVICE_API_KEY_PEPPER` is configured. These endpoints report gate decisions; they do not drive an actual servo or other hardware.

The abuse controls are practical server-side defenses (rate limits, login throttling, account purchase caps, idempotency and strict request decoding); they are not a CAPTCHA or a guarantee against distributed automated abuse. Tune limits and put the service behind TLS and a trusted proxy.

## Quai setup

`quai-bridge/deploy.mjs --compile-only` compiles the contract. To deploy, set `QUAI_RPC_URL` and `QUAI_PRIVATE_KEY` in the bridge/deployment environment and run `node quai-bridge/deploy.mjs`; copy the emitted address into `QUAI_CONTRACT_ADDRESS`. Use the matching chain endpoint and configuration from the [official Quai docs](https://docs.qu.ai/sdk/static/provider). For local compose, set a bridge bearer token of at least 32 random characters. Leaving Quai credentials empty disables chain jobs while online booking remains usable.

## Remaining production work

The project still needs a complete paid resale checkout lifecycle, SMTP outbox delivery, venue/admin provisioning APIs, event update/cancellation workflows, and database-backed integration tests against migrations and real PostgreSQL. The transfer endpoint supports owner-to-owner ticket transfers; it does not charge a resale buyer. Hardware integration remains explicitly postponed. OpenAPI is in `docs/openapi.yaml` and should be kept aligned as routes evolve.
