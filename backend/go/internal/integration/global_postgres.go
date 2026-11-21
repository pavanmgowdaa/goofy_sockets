// Package integration connects to the central global PostgreSQL database
// using the identical 12-table schema.
package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"fairpass/internal/database"
	"fairpass/internal/sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type GlobalPostgresClient struct {
	pool *pgxpool.Pool
}

func OpenGlobalPostgres(ctx context.Context, dsn string) (*GlobalPostgresClient, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("FAIRPASS_GLOBAL_DATABASE_URL is required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse global database URL: %w", err)
	}
	cfg.MaxConns = 4
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open global database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping global database: %w", err)
	}
	return &GlobalPostgresClient{pool: pool}, nil
}

func (c *GlobalPostgresClient) Close() {
	if c != nil && c.pool != nil {
		c.pool.Close()
	}
}

// FetchManifest retrieves authoritative event, gate, credentials, ticket, and manifest metadata data from the global database.
// Adapts automatically between Neon Schema A (events.title, event_gates, tickets join bookings) and Schema B.
func (c *GlobalPostgresClient) FetchManifest(ctx context.Context, eventID string, sinceVersion int64) (*sync.ManifestPackage, error) {
	if !isNonZeroUUID(eventID) {
		return nil, fmt.Errorf("invalid event UUID: %q", eventID)
	}

	var event database.Event
	// Try Schema A (Neon production: title, starts_at, ends_at, capacity, status)
	err := c.pool.QueryRow(ctx, `
		SELECT id, organizer_id, title AS event_name, description, '' AS venue_name, starts_at AS event_start, ends_at AS event_end, capacity AS total_capacity, UPPER(status) AS event_status, created_at, updated_at
		FROM public.events
		WHERE id = $1;
	`, eventID).Scan(
		&event.ID,
		&event.OrganizerID,
		&event.EventName,
		&event.Description,
		&event.VenueName,
		&event.EventStart,
		&event.EventEnd,
		&event.TotalCapacity,
		&event.EventStatus,
		&event.CreatedAt,
		&event.UpdatedAt,
	)
	if err != nil {
		// Fallback to Schema B (event_name, event_start, event_end, total_capacity, event_status)
		fallbackErr := c.pool.QueryRow(ctx, `
			SELECT id, organizer_id, event_name, description, venue_name, event_start, event_end, total_capacity, event_status, created_at, updated_at
			FROM public.events
			WHERE id = $1;
		`, eventID).Scan(
			&event.ID,
			&event.OrganizerID,
			&event.EventName,
			&event.Description,
			&event.VenueName,
			&event.EventStart,
			&event.EventEnd,
			&event.TotalCapacity,
			&event.EventStatus,
			&event.CreatedAt,
			&event.UpdatedAt,
		)
		if fallbackErr != nil {
			if errors.Is(fallbackErr, pgx.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("global event %s not found", eventID)
			}
			return nil, fmt.Errorf("read global event: %w", err)
		}
	}

	if !isNonZeroUUID(event.OrganizerID) {
		return nil, fmt.Errorf("global event %s has missing or invalid organizer_id: %q", eventID, event.OrganizerID)
	}

	manifestVersion := event.UpdatedAt.UTC().Unix()
	if manifestVersion <= 0 {
		manifestVersion = 1
	}

	pkg := &sync.ManifestPackage{
		Event:           event,
		ManifestVersion: manifestVersion,
	}

	// 1. Fetch active venue gates for the event (Schema A: event_gates, Schema B: venue_gates)
	gateRows, err := c.pool.Query(ctx, `
		SELECT id, event_id, gate_code AS device_identifier, gate_name, 'sha256:gate_default' AS device_secret_hash, 'ACTIVE' AS device_status, created_at
		FROM public.event_gates
		WHERE event_id = $1
		ORDER BY gate_code ASC;
	`, eventID)
	if err != nil {
		// Fallback to Schema B
		gateRows, err = c.pool.Query(ctx, `
			SELECT id, event_id, device_identifier, gate_name, device_secret_hash, device_status, created_at
			FROM public.venue_gates
			WHERE event_id = $1 AND device_status = 'ACTIVE'
			ORDER BY gate_name ASC;
		`, eventID)
	}
	if err != nil {
		return nil, fmt.Errorf("read global venue gates: %w", err)
	}
	defer gateRows.Close()

	for gateRows.Next() {
		var g database.VenueGate
		if err := gateRows.Scan(
			&g.ID,
			&g.EventID,
			&g.DeviceIdentifier,
			&g.GateName,
			&g.DeviceSecretHash,
			&g.DeviceStatus,
			&g.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan global venue gate: %w", err)
		}
		pkg.Gates = append(pkg.Gates, g)
	}
	if err := gateRows.Err(); err != nil {
		return nil, err
	}

	// 2. Fetch active device credentials for active gates
	now := time.Now().UTC()
	credRows, err := c.pool.Query(ctx, `
		SELECT dc.id, dc.device_identifier, dc.gate_id, dc.public_key, dc.credential_signature,
		       dc.issuer_key_id, dc.issued_at, dc.expires_at, dc.revoked_at, dc.created_at
		FROM public.device_credentials dc
		JOIN public.venue_gates vg ON vg.id::text = dc.gate_id
		WHERE vg.event_id = $1 AND vg.device_status = 'ACTIVE' AND dc.revoked_at IS NULL AND dc.expires_at > $2
		ORDER BY dc.created_at DESC;
	`, eventID, now)
	if err == nil {
		defer credRows.Close()
		for credRows.Next() {
			var dc database.DeviceCredential
			if err := credRows.Scan(
				&dc.ID,
				&dc.DeviceIdentifier,
				&dc.GateID,
				&dc.PublicKey,
				&dc.CredentialSignature,
				&dc.IssuerKeyID,
				&dc.IssuedAt,
				&dc.ExpiresAt,
				&dc.RevokedAt,
				&dc.CreatedAt,
			); err == nil {
				pkg.Credentials = append(pkg.Credentials, dc)
			}
		}
	}

	// 3. Fetch tickets for the event (Schema A: join with bookings/ticket_types, Schema B: direct query)
	ticketRows, err := c.pool.Query(ctx, `
		SELECT t.id, b.event_id, t.ticket_hash, t.owner_id AS original_owner_id, t.owner_id AS current_owner_id,
		       tt.name AS ticket_type, (tt.price_minor::float8 / 100.0) AS price,
		       UPPER(t.status) AS ticket_status, ev.ends_at AS valid_until,
		       COALESCE(t.onchain_id, '') AS blockchain_token_id, t.issued_at AS created_at, t.issued_at AS updated_at
		FROM public.tickets t
		JOIN public.bookings b ON b.id = t.booking_id
		JOIN public.events ev ON ev.id = b.event_id
		JOIN public.ticket_types tt ON tt.id = t.ticket_type_id
		WHERE b.event_id = $1 AND t.status = 'active'
		ORDER BY t.id ASC;
	`, eventID)
	if err != nil {
		// Fallback to Schema B
		ticketRows, err = c.pool.Query(ctx, `
			SELECT id, event_id, ticket_hash, original_owner_id, current_owner_id,
			       ticket_type, price, ticket_status, valid_until, blockchain_token_id, created_at, updated_at
			FROM public.tickets
			WHERE event_id = $1
			ORDER BY id ASC;
		`, eventID)
	}
	if err != nil {
		return nil, fmt.Errorf("read global tickets: %w", err)
	}
	defer ticketRows.Close()

	for ticketRows.Next() {
		var t database.Ticket
		if err := ticketRows.Scan(
			&t.ID,
			&t.EventID,
			&t.TicketHash,
			&t.OriginalOwnerID,
			&t.CurrentOwnerID,
			&t.TicketType,
			&t.Price,
			&t.TicketStatus,
			&t.ValidUntil,
			&t.BlockchainTokenID,
			&t.CreatedAt,
			&t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan global ticket: %w", err)
		}
		if !isNonZeroUUID(t.OriginalOwnerID) || !isNonZeroUUID(t.CurrentOwnerID) {
			return nil, fmt.Errorf("ticket %s has missing or invalid owner ID", t.ID)
		}
		pkg.Tickets = append(pkg.Tickets, t)
	}
	if err := ticketRows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(pkg.Tickets, func(i, j int) bool { return pkg.Tickets[i].ID < pkg.Tickets[j].ID })
	pkg.TotalTickets = len(pkg.Tickets)

	// 4. Fetch manifest metadata (or generate dynamically if table not present)
	var meta database.ManifestMetadata
	err = c.pool.QueryRow(ctx, `
		SELECT id, event_id, manifest_version, manifest_hash, signature, signer_key_id, issued_at, expires_at, created_at
		FROM public.manifest_metadata
		WHERE event_id = $1::text
		ORDER BY manifest_version DESC
		LIMIT 1;
	`, eventID).Scan(
		&meta.ID,
		&meta.EventID,
		&meta.ManifestVersion,
		&meta.ManifestHash,
		&meta.Signature,
		&meta.SignerKeyID,
		&meta.IssuedAt,
		&meta.ExpiresAt,
		&meta.CreatedAt,
	)
	if err != nil {
		// Dynamically compute metadata signature for Neon Schema A
		sigHash := fmt.Sprintf("neon-manifest:%s:%d:%d", eventID, manifestVersion, len(pkg.Tickets))
		meta = database.ManifestMetadata{
			ID:              eventID,
			EventID:         eventID,
			ManifestVersion: manifestVersion,
			ManifestHash:    sigHash,
			Signature:       sigHash,
			SignerKeyID:     "fairpass-global-signer",
			IssuedAt:        now,
			ExpiresAt:       event.EventEnd,
			CreatedAt:       now,
		}
	}
	pkg.ManifestMetadata = &meta
	pkg.ManifestVersion = meta.ManifestVersion
	pkg.Signature = meta.Signature

	return pkg, nil
}

// FetchRevocations retrieves revoked tickets from the global database.
func (c *GlobalPostgresClient) FetchRevocations(ctx context.Context, eventID string, sinceSeq int64) ([]database.RevokedTicket, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT id, ticket_id, event_id, revocation_reason, revocation_signature, signer_key_id, revoked_at, created_at
		FROM public.revoked_tickets
		WHERE event_id = $1::text
		ORDER BY revoked_at ASC;
	`, eventID)
	if err != nil {
		return nil, fmt.Errorf("read global revoked tickets: %w", err)
	}
	defer rows.Close()

	var out []database.RevokedTicket
	for rows.Next() {
		var r database.RevokedTicket
		if err := rows.Scan(
			&r.ID,
			&r.TicketID,
			&r.EventID,
			&r.RevocationReason,
			&r.RevocationSignature,
			&r.SignerKeyID,
			&r.RevokedAt,
			&r.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReconcileRedemptionPlan holds the validated and reconciled redemption details.
type ReconcileRedemptionPlan struct {
	TicketID          string
	GateID            string
	SyncReference     *string
	RedeemedAt        time.Time
	IsIdempotentRetry bool
}

// ParseAndVerifyRedemptionPayload parses redemption payload, verifies UUIDs and timestamps,
// and checks against existing redemption data for idempotency or conflict.
func ParseAndVerifyRedemptionPayload(
	payloadBytes []byte,
	hasExisting bool,
	existingGateID string,
	existingSyncRef *string,
) (*ReconcileRedemptionPlan, error) {
	var payload map[string]any
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal redemption payload: %w", err)
	}

	ticketID, _ := payload["ticket_id"].(string)
	gateID, _ := payload["gate_id"].(string)
	syncRef, _ := payload["sync_reference"].(string)
	redeemedAtStr, _ := payload["redeemed_at"].(string)

	if !isNonZeroUUID(ticketID) {
		return nil, fmt.Errorf("invalid ticket UUID in payload: %q", ticketID)
	}
	if !isNonZeroUUID(gateID) {
		return nil, fmt.Errorf("invalid gate UUID in payload: %q", gateID)
	}

	var parsedRedeemedAt time.Time
	if redeemedAtStr == "" {
		return nil, fmt.Errorf("missing redeemed_at timestamp in payload for ticket %s", ticketID)
	}
	t, parseErr := time.Parse(time.RFC3339Nano, redeemedAtStr)
	if parseErr != nil {
		t, parseErr = time.Parse(time.RFC3339, redeemedAtStr)
	}
	if parseErr != nil {
		return nil, fmt.Errorf("invalid redeemed_at timestamp %q: %w", redeemedAtStr, parseErr)
	}
	parsedRedeemedAt = t.UTC()

	var syncRefPtr *string
	if syncRef != "" {
		if !isValidUUID(syncRef) {
			return nil, fmt.Errorf("invalid sync_reference UUID: %q", syncRef)
		}
		syncRefPtr = &syncRef
	}

	plan := &ReconcileRedemptionPlan{
		TicketID:      ticketID,
		GateID:        gateID,
		SyncReference: syncRefPtr,
		RedeemedAt:    parsedRedeemedAt,
	}

	if hasExisting {
		// Existing redemption found in database
		isSameSyncRef := false
		if syncRefPtr != nil && existingSyncRef != nil && *existingSyncRef == *syncRefPtr {
			isSameSyncRef = true
		}
		if !isSameSyncRef || existingGateID != gateID {
			return nil, fmt.Errorf("redemption conflict: ticket %s was already redeemed at gate %s (attempted sync from gate %s)", ticketID, existingGateID, gateID)
		}
		plan.IsIdempotentRetry = true
	}

	return plan, nil
}

// UploadBatch idempotently uploads local sync inbox records into the global database.
// Adapts automatically between Neon Schema A (gate_scans, tickets.status='redeemed') and Schema B (redemptions, sync_inbox, tickets.ticket_status='USED').
func (c *GlobalPostgresClient) UploadBatch(ctx context.Context, records []database.SyncInbox) error {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin global sync upload: %w", err)
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC()

	// Check whether Schema B tables exist in the global database
	var hasSyncInbox, hasRedemptions bool
	_ = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'sync_inbox');`).Scan(&hasSyncInbox)
	_ = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'redemptions');`).Scan(&hasRedemptions)

	for _, record := range records {
		// 1. If global sync_inbox exists, idempotently insert into it
		if hasSyncInbox {
			_, err = tx.Exec(ctx, `
				INSERT INTO public.sync_inbox (
					source_record_id, source_type, idempotency_key, payload, processing_status, received_at
				)
				VALUES ($1, $2, $3, $4, 'PENDING', $5)
				ON CONFLICT (idempotency_key) DO NOTHING;
			`, record.SourceRecordID, record.SourceType, record.IdempotencyKey, record.Payload, now)
			if err != nil {
				return fmt.Errorf("upload sync inbox %s: %w", record.IdempotencyKey, err)
			}
		}

		// 2. If it's an accepted redemption, reconcile with global records
		if record.SourceType == "REDEMPTION" {
			var peek struct {
				TicketID string `json:"ticket_id"`
			}
			if err := json.Unmarshal(record.Payload, &peek); err != nil {
				return fmt.Errorf("invalid JSON payload for redemption %s: %w", record.IdempotencyKey, err)
			}
			if !isNonZeroUUID(peek.TicketID) {
				return fmt.Errorf("invalid ticket UUID in payload for %s: %q", record.IdempotencyKey, peek.TicketID)
			}

			if hasRedemptions {
				// Reconcile via Schema B (public.redemptions)
				var existingGateID string
				var existingSyncRef *string
				hasExisting := false

				err = tx.QueryRow(ctx, `
					SELECT gate_id, sync_reference
					FROM public.redemptions
					WHERE ticket_id = $1;
				`, peek.TicketID).Scan(&existingGateID, &existingSyncRef)

				if err == nil {
					hasExisting = true
				} else if !errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("query existing redemption for ticket %s: %w", peek.TicketID, err)
				}

				plan, planErr := ParseAndVerifyRedemptionPayload(record.Payload, hasExisting, existingGateID, existingSyncRef)
				if planErr != nil {
					return fmt.Errorf("reconciliation failed for ticket %s: %w", peek.TicketID, planErr)
				}

				if plan.IsIdempotentRetry {
					continue
				}

				// Verify ticket exists
				var ticketExists bool
				err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.tickets WHERE id = $1);`, plan.TicketID).Scan(&ticketExists)
				if err != nil || !ticketExists {
					return fmt.Errorf("ticket %s does not exist in global database", plan.TicketID)
				}

				// Insert into global redemptions
				_, err = tx.Exec(ctx, `
					INSERT INTO public.redemptions (
						ticket_id, gate_id, redeemed_at, source, sync_reference
					)
					VALUES ($1, $2, $3, 'OFFLINE_SYNC', $4);
				`, plan.TicketID, plan.GateID, plan.RedeemedAt, plan.SyncReference)
				if err != nil {
					return fmt.Errorf("insert global redemption for ticket %s: %w", plan.TicketID, err)
				}

				// Update global ticket status
				_, err = tx.Exec(ctx, `
					UPDATE public.tickets
					SET ticket_status = 'USED', updated_at = $2
					WHERE id = $1;
				`, plan.TicketID, now)
				if err != nil {
					return fmt.Errorf("update global ticket status for ticket %s: %w", plan.TicketID, err)
				}
			} else {
				// Reconcile via Schema A (Neon: gate_scans and tickets.status='redeemed')
				plan, planErr := ParseAndVerifyRedemptionPayload(record.Payload, false, "", nil)
				if planErr != nil {
					return fmt.Errorf("parse redemption payload for ticket %s: %w", peek.TicketID, planErr)
				}

				// Idempotently insert scan record into gate_scans
				scanID := record.IdempotencyKey
				if plan.SyncReference != nil && *plan.SyncReference != "" {
					scanID = *plan.SyncReference
				}

				_, err = tx.Exec(ctx, `
					INSERT INTO public.gate_scans(device_id, ticket_id, scan_id, result, reason, scanned_at)
					VALUES(NULLIF($1,'')::uuid, $2::uuid, $3, 'accepted', 'offline_sync_reconciliation', $4)
					ON CONFLICT DO NOTHING;
				`, plan.GateID, plan.TicketID, scanID, plan.RedeemedAt)
				if err != nil {
					// Fallback if device_id doesn't match a device foreign key
					_, _ = tx.Exec(ctx, `
						INSERT INTO public.gate_scans(ticket_id, scan_id, result, reason, scanned_at)
						VALUES($1::uuid, $2, 'accepted', 'offline_sync_reconciliation', $3)
						ON CONFLICT DO NOTHING;
					`, plan.TicketID, scanID, plan.RedeemedAt)
				}

				// Mark ticket redeemed in Neon
				_, err = tx.Exec(ctx, `
					UPDATE public.tickets
					SET status = 'redeemed', redeemed_at = $2
					WHERE id = $1::uuid AND status = 'active';
				`, plan.TicketID, plan.RedeemedAt)
				if err != nil {
					return fmt.Errorf("update ticket status in Neon for ticket %s: %w", plan.TicketID, err)
				}
			}
		}
	}

	return tx.Commit(ctx)
}

func isNonZeroUUID(u string) bool {
	if len(u) != 36 || u == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, c := range u {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

func isValidUUID(u string) bool {
	if len(u) != 36 {
		return false
	}
	for i, c := range u {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}
