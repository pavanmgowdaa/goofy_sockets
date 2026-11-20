# FAIRPASS backend

Go REST API foundation for FAIRPASS, backed by PostgreSQL and pgx/v5. The implementation includes account registration/login and rotating refresh tokens, role checks, event creation/listing, inventory-reserving bookings, Razorpay order creation and signed webhook processing, ticket issuance with high-entropy QR credentials, device-authenticated gate scans, sensor ingestion, audit records, and a minimal Quai Solidity ticket registry.

## Run locally

1. Copy `.env.example` to `.env`, set `DATABASE_URL` and a random `JWT_SECRET` of at least 32 characters.
2. Start PostgreSQL and apply `migrations/000001_init.up.sql` (or use `docker compose up --build`).
3. Run `go run ./cmd/api`.

The API listens on `:8080`; `GET /healthz` checks database availability. Use `Authorization: Bearer <access_token>` for user routes and `X-Device-Key` for gate routes. Device key hashes are stored with `SHA256(key + DEVICE_API_KEY_PEPPER)`; provision devices through a trusted administrative process. Never commit `.env` or private keys.

## API overview

OpenAPI is in `docs/openapi.yaml`. Main routes: `POST /v1/auth/register`, `/v1/auth/login`, `/v1/auth/refresh`; `GET /v1/me`; `GET/POST /v1/events`; `POST /v1/events/{id}/publish`; `POST /v1/bookings` with `Idempotency-Key` equivalent JSON field; `POST /v1/bookings/{id}/payment-order`; Razorpay webhook at `/v1/payments/razorpay/webhook`; gate routes `/v1/gate/verify`, `/v1/gate/readings`, `/v1/gate/heartbeat`.

Create ticket types using the database/admin provisioning path until a dedicated organizer endpoint is added. The notification table is an outbox; no SMTP delivery worker is included yet. Payment capture webhook is the sole path that marks a booking paid and issues tickets.

## Quai integration boundary

Current official Quai documentation describes EVM-compatible zone chains and Quai-specific RPC behavior. It documents `quais` as the supported programmatic SDK; no maintained Go-compatible Quai SDK was verified. The contract in `contracts/FairPassTickets.sol` stores only ticket IDs/hashes and supports owner verification, transfers, and redemption. Contract deployment, transaction signing, confirmation polling, and retry worker must be wired through a separately verified Quai signer/provider before enabling production on-chain writes. Orchard testnet docs currently identify chain ID 15000 and `https://orchard.rpc.quai.network/cyprus1`; mainnet examples identify chain ID 9 and `https://rpc.quai.network/cyprus1`.

## Limitations to close before production

This is a backend foundation, not a security-audited full platform. It needs an admin provisioning interface for gate devices, venues, ticket types and user verification; payment refund/resale/transfer endpoints and workers; cleanup of expired reservations; robust per-IP and per-account rate limiting/login throttling; SMTP outbox delivery; QR token delivery UX; and end-to-end migration, integration, and contract tests. Run behind TLS and a trusted proxy, use secret management, and review data retention and operational monitoring.
