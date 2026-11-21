# FAIRPASS: Offline Gate Architecture & Go Backend

FAIRPASS is an offline-capable, high-reliability event ticketing and gate management platform. This repository contains the local PostgreSQL database schema, migrations, device authentication, atomic ticket validation, and synchronization engine for venue-side deployment.

---

## 1. Architectural Overview

At the event venue, **ONE shared local PostgreSQL server** (`fairpass`) acts as the single source of truth during offline operation. Multiple physical turnstiles/gate scanning devices communicate with this server over local Wi-Fi or LAN.

```
                           GLOBAL CLOUD (Neon PostgreSQL)
                                         ▲
                                         │ (Async Periodic Sync via Outbox & Checkpoints)
                                         ▼
                             LOCAL VENUE DATABASE (PostgreSQL)
                               Database Name: `fairpass`
                                         ▲
                         ┌───────────────┼───────────────┐
                         ▼               ▼               ▼
                   [Gate A Device]  [Gate B Device]  [Gate C Device]
                   (LAN / Wi-Fi)    (LAN / Wi-Fi)    (LAN / Wi-Fi)
```

- **Global Database (Neon PostgreSQL)**: Authoritative for users, bookings, payments, initial gate allocation, and ticket issuance.
- **Local Database (PostgreSQL)**: Authoritative during the event for gate access, offline validation, device authentication, duplicate prevention, sensor telemetry, and outbox queuing.

---

## 2. Local Database Schema (12 Tables)

The complete local database comprises 12 tables defined in `database/local/schema.sql`:

| Table | Purpose | Key Relationships |
|---|---|---|
| `edge_events` | Cached event schedule and manifest signatures. | Root event record. |
| `gate_devices` | Registered physical handheld and turnstile scanners. | References `event_gate_devices`, `iot_readings`. |
| `event_gates` | Physical gates belonging to an event. | References `edge_events(id)`. |
| `event_gate_devices` | Assigns scanning devices to event-specific gates. | Composite FK `(event_gate_id, event_id)` to `event_gates(id, event_id)`. |
| `ticket_manifest` | Downloaded tickets authorized for offline validation. | Composite FK `(assigned_gate_id, event_id)` to `event_gates(id, event_id)`. |
| `gate_scans` | Immutable log of all scan attempts (accepted & rejected). | References `ticket_manifest`, `gate_devices`, and composite FK to `event_gates`. Partial unique index `unique_accepted_ticket` on `(ticket_id) WHERE (accepted = true)`. |
| `iot_readings` | Turnstile hardware and environmental telemetry. | References `gate_devices(id)`. |
| `sync_outbox` | Queue of scans and telemetry waiting for cloud upload. | Indexed on `(created_at) WHERE (sync_status = 'PENDING')`. |
| `manifest_metadata` | Cryptographic signatures and metadata of manifests. | Standardized `event_id UUID` referencing `edge_events(id)`. |
| `sync_checkpoints` | Resume points for manifest and revocation synchronization. | Unique on `sync_type`. |
| `revoked_tickets` | Tickets cancelled/refunded after manifest download. | References `edge_events(id)`. |
| `device_credentials` | Public keys and credential hashes for scanners. | References `gate_devices(id)`. |

---

## 3. Database Migration & Fresh Setup

### Applying Migration 002 (Gate Allocation)
To upgrade an existing local `fairpass` database:

```bash
# Set your PostgreSQL credentials
export PGPASSWORD="your_local_postgres_password"

# Execute migration
psql -h localhost -p 5432 -U postgres -d fairpass -f database/local/migrations/002_gate_allocation.sql
```

### Fresh Installation
To deploy a new local database from scratch:

```bash
# 1. Create the database
createdb -h localhost -p 5432 -U postgres fairpass

# 2. Run the complete schema
psql -h localhost -p 5432 -U postgres -d fairpass -f database/local/schema.sql
```

---

## 4. Environment Variables

Configure your local environment via system variables or a local `.env` file (never committed to version control):

| Variable | Description | Default |
|---|---|---|
| `FAIRPASS_ADDR` | Server listening address and port | `:8443` |
| `FAIRPASS_TLS_CERT` | Path to server TLS certificate | `certs/server-cert.pem` |
| `FAIRPASS_TLS_KEY` | Path to server TLS private key | `certs/server-key.pem` |
| `FAIRPASS_DB_HOST` | Local PostgreSQL host | `127.0.0.1` |
| `FAIRPASS_DB_PORT` | Local PostgreSQL port | `5432` |
| `FAIRPASS_DB_NAME` | Local PostgreSQL database name | `fairpass` |
| `FAIRPASS_DB_USER` | Local PostgreSQL username | `postgres` |
| `FAIRPASS_DB_PASSWORD` | Local PostgreSQL password | *(empty)* |
| `FAIRPASS_DB_SSLMODE` | SSL connection mode | `disable` |
| `FAIRPASS_DB_MAX_CONNS`| Maximum pool connections | `25` |
| `FAIRPASS_SECRET_KEY` | HMAC cryptographic verification secret | `fairpass-venue-offline-secret-key` |

---

## 5. Go Backend Setup & Execution

### Generating Local TLS Certificates
```bash
go run generate-cert.go
```
This generates `certs/server-cert.pem` and `certs/server-key.pem`.

### Running the Server
```bash
cd backend/go
go run ./cmd/server
```
Endpoints surfaced:
- `GET /health`: Health check reporting HTTPS protocol, timestamp, and database connectivity.
- `GET /api/v1/status`: Operational service status.
- `POST /api/v1/scan`: Validates tickets against assigned gates with atomic duplicate prevention.
- `POST /api/v1/sync/outbox`: Manually flushes the pending local outbox queue.

---

## 6. Offline Ticket Validation Sequence

When a gate device scans a ticket QR code, it submits a request to `POST /api/v1/scan`:

```json
{
  "device_identifier": "GATE-DEV-01",
  "device_secret": "secure_device_secret_hash",
  "ticket_id": "4b92b6a2-671e-455b-bf7b-f9d22fa97c9b",
  "signature": "hmac_signature_hex"
}
```

The 12-step verification pipeline runs within a single atomic PostgreSQL transaction:
1. **Device Authentication**: Authenticates `device_identifier` and validates secret hash.
2. **Device Status**: Verifies scanner status is `ACTIVE`.
3. **Active Gate Assignment**: Identifies device's active gate assignment (`revoked_at IS NULL`) for the event.
4. **Signature Verification**: Verifies ticket cryptographic signature using venue secret key.
5. **Event Schedule**: Verifies current time falls within event `valid_from` and `valid_until`.
6. **Manifest Validity**: Checks manifest expiration timestamp.
7. **Revocation Check**: Checks `revoked_tickets` table for cancellations or refunds.
8. **Ticket State**: Checks `ticket_manifest.ticket_status` (must be `ACTIVE`).
9. **Gate Alignment**: Verifies `ticket_manifest.assigned_gate_id == scanning_gate_id`. If mismatched, returns `WRONG_GATE` and records the rejected attempt without consuming the ticket.
10. **Duplicate Scan Check**: Verifies no prior accepted scan exists in `gate_scans`.
11. **Atomic Consumption**: Inserts into `gate_scans` with `accepted = true` and updates `ticket_manifest.ticket_status = 'USED'`. Protected by PostgreSQL row-level locks (`FOR UPDATE`) and the `unique_accepted_ticket` partial index.
12. **Outbox Synchronization**: Inserts the synchronization record into `sync_outbox` within the exact same transaction.

### Scan Response Codes
- `VALID`: Entry granted.
- `DUPLICATE`: Ticket already accepted or scanned at another gate.
- `WRONG_GATE`: Valid ticket presented at an unassigned gate.
- `REVOKED`: Ticket was cancelled or refunded post-booking.
- `CANCELLED`: Ticket marked cancelled in manifest.
- `EXPIRED`: Event or ticket validity window has elapsed.
- `INVALID`: Unknown ticket or tampered cryptographic signature.
- `DEVICE_UNAUTHORIZED`: Unregistered or deactivated scanner.

---

## 7. Atomic Duplicate Prevention

When multiple gate devices scan the same ticket simultaneously:
1. Both requests begin a transaction with `SELECT ... FOR UPDATE` on `ticket_manifest`.
2. PostgreSQL locks the ticket row for the first transaction.
3. The first transaction verifies status, records `gate_scans` (`accepted = true`), changes status to `USED`, queues to `sync_outbox`, and commits.
4. The second transaction unblocks, reads `ticket_status == 'USED'`, and immediately returns `DUPLICATE`.
5. If concurrent transactions race past row-level checks, the database constraint `unique_accepted_ticket` (`UNIQUE INDEX ON gate_scans (ticket_id) WHERE (accepted = true)`) guarantees only one accepted scan can physically commit. The Go backend catches error `23505` and returns `DUPLICATE`.

---

## 8. Offline Synchronization Architecture

1. **Downstream Ingestion**:
   - Manifests, gate allocation maps, device assignments, and revocations are downloaded from the global Neon database prior to the event.
   - Updates are applied idempotently with cryptographic verification.
   - `sync_checkpoints` tracks sequence watermarks.
2. **Upstream Upload**:
   - Accepted and rejected scans are recorded in `sync_outbox`.
   - The background worker polls `sync_outbox` periodically.
   - When venue connectivity is active, batches are pushed to Neon and marked `SYNCED`.
   - If the internet is severed, operations continue unhindered locally without data loss. Queued records automatically resume uploading upon reconnection.

---

## 9. Automated Testing

Run the automated test suite covering all 14 architectural scenarios:

```bash
cd backend/go
go test -v ./internal/validation ./internal/sync
```

Scenarios verified:
- [x] Valid ticket scanned at assigned gate.
- [x] Ticket scanned at wrong gate (rejection preserved without consuming ticket).
- [x] Ticket already accepted (duplicate prevention).
- [x] Cancelled ticket rejection.
- [x] Revoked ticket rejection.
- [x] Expired ticket rejection.
- [x] Invalid ticket signature.
- [x] Unauthorized gate device rejection.
- [x] Concurrent gate devices scanning the same ticket simultaneously (atomic race test).
- [x] Internet disconnection during event (local operation continues).
- [x] Synchronization resuming after reconnection.
- [x] Duplicate synchronization uploads (idempotency check).
- [x] Gate-device reassignment and revocation.
- [x] Event-specific gate validation.
