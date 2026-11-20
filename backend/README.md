# FAIRPASS backend

Go REST API foundation for FAIRPASS, backed by PostgreSQL and pgx/v5. The implementation includes account registration/login and rotating refresh tokens, role checks, event creation/listing, inventory-reserving bookings, Razorpay order creation and signed webhook processing, ticket issuance with high-entropy QR credentials, device-authenticated gate scans, sensor ingestion, audit records, and a minimal Quai Solidity ticket registry.

## Run locally

1. Copy `.env.example` to `.env`, set `DATABASE_URL` and a random `JWT_SECRET` of at least 32 characters.
2. For a new database, use `docker compose up --build`. For an existing global database, review and apply `migrations/000002_booking_reservations.up.sql` through your migration process before deploying this code. This additive migration adds a nullable expiry column and index; no migration was executed here.
3. Run `go run ./cmd/api`.

The API listens on `:8080`; `GET /healthz` checks database availability. Use `Authorization: Bearer <access_token>` for user routes and `X-Device-Key` for gate routes. Device key hashes are stored with `SHA256(key + DEVICE_API_KEY_PEPPER)`; provision devices through a trusted administrative process. Never commit `.env` or private keys.

## API overview

OpenAPI is in `docs/openapi.yaml`. Main routes: `POST /v1/auth/register`, `/v1/auth/login`, `/v1/auth/refresh`; `GET /v1/me`; `GET/POST /v1/events`; `GET /v1/events/{id}/ticket-types`; `POST /v1/events/{id}/publish`; `POST /v1/bookings` with an idempotency key in JSON; `POST /v1/bookings/{id}/payment-order`; Razorpay webhook at `/v1/payments/razorpay/webhook`.

Organizers can create ticket types on draft events; total ticket inventory is capped by event capacity. New reservations expire after 15 minutes. Expired inventory is released transactionally before the next booking attempt, and stale reservations are excluded from the public availability listing. Failed payment webhooks release reservations. Capture webhooks validate amount and currency, issue tickets once, and place late captures into the refund review outbox. The notification table is an outbox; SMTP delivery and automated refunds are not implemented. QR and refresh credentials are random 256-bit tokens stored as SHA-256 digests; passwords use bcrypt.

The edge database dump at `../database/local/schema.sql` is a separate manifest and offline scan store. This change does not alter or merge its schema with the global online database and does not extend hardware integration.

## Quai integration boundary

Current official Quai documentation describes EVM-compatible zone chains and Quai-specific RPC behavior. It documents `quais` as the supported programmatic SDK; no maintained Go-compatible Quai SDK was verified. The contract in `contracts/FairPassTickets.sol` stores only ticket IDs/hashes and supports owner verification, transfers, and redemption. Contract deployment, transaction signing, confirmation polling, and retry worker must be wired through a separately verified Quai signer/provider before enabling production on-chain writes. Orchard testnet docs currently identify chain ID 15000 and `https://orchard.rpc.quai.network/cyprus1`; mainnet examples identify chain ID 9 and `https://rpc.quai.network/cyprus1`.

## Limitations to close before production

This is a backend foundation, not a security-audited full platform. It needs an admin provisioning interface for venues and user verification; automated Razorpay refund handling for late captures; resale/transfer endpoints and workers; an independent periodic reservation cleanup job; distributed per-IP and per-account rate limiting/login throttling; SMTP outbox delivery; QR token delivery UX; and end-to-end database/migration, integration, and contract tests. Run behind TLS and a trusted proxy, use secret management, and review data retention and operational monitoring.
