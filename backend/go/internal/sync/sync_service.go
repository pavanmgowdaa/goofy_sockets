package sync

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"time"

	"fairpass/internal/database"

	"github.com/jackc/pgx/v5"
)

var (
	// ErrSyncUnavailable is returned when synchronization is attempted without an active global database connection.
	ErrSyncUnavailable = errors.New("upstream synchronization service is unavailable: no connection to global database")
)

// UpstreamClient defines the interface for communicating with the central database.
type UpstreamClient interface {
	UploadBatch(ctx context.Context, records []database.SyncInbox) error
	FetchManifest(ctx context.Context, eventID string, sinceVersion int64) (*ManifestPackage, error)
	FetchRevocations(ctx context.Context, eventID string, sinceSeq int64) ([]database.RevokedTicket, error)
}

// MockUpstreamClient implements UpstreamClient for tests only.
type MockUpstreamClient struct {
	ShouldFail bool
	Uploaded   []database.SyncInbox
}

func (m *MockUpstreamClient) UploadBatch(ctx context.Context, records []database.SyncInbox) error {
	if m.ShouldFail {
		return errors.New("upstream central database is unreachable (offline mode)")
	}
	m.Uploaded = append(m.Uploaded, records...)
	return nil
}

func (m *MockUpstreamClient) FetchManifest(ctx context.Context, eventID string, sinceVersion int64) (*ManifestPackage, error) {
	if m.ShouldFail {
		return nil, errors.New("upstream database is unreachable")
	}
	return nil, nil
}

func (m *MockUpstreamClient) FetchRevocations(ctx context.Context, eventID string, sinceSeq int64) ([]database.RevokedTicket, error) {
	if m.ShouldFail {
		return nil, errors.New("upstream database is unreachable")
	}
	return nil, nil
}

// ManifestPackage contains authoritative event details, gates, and tickets.
type ManifestPackage struct {
	Event            database.Event              `json:"event"`
	Gates            []database.VenueGate        `json:"gates"`
	Credentials      []database.DeviceCredential `json:"credentials,omitempty"`
	Tickets          []database.Ticket           `json:"tickets"`
	ManifestMetadata *database.ManifestMetadata  `json:"manifest_metadata,omitempty"`
	TotalTickets     int                         `json:"total_tickets"`
	ManifestVersion  int64                       `json:"manifest_version"`
	Signature        string                      `json:"signature"`
}

// Service manages synchronization operations between local PostgreSQL and upstream.
type Service struct {
	db        *database.DB
	upstream  UpstreamClient
	secretKey string
}

// NewSyncService creates a new Service instance.
// If upstream is nil, synchronization remains unavailable (offline mode).
// Mock clients must only be provided explicitly in unit tests.
func NewSyncService(db *database.DB, upstream UpstreamClient, secretKey string) *Service {
	if secretKey == "" {
		secretKey = "fairpass-venue-offline-secret-key"
	}
	return &Service{
		db:        db,
		upstream:  upstream,
		secretKey: secretKey,
	}
}

// IsUpstreamAvailable reports whether an authoritative upstream connection is configured.
func (s *Service) IsUpstreamAvailable() bool {
	return s != nil && s.upstream != nil
}

// ProcessOutbox processes pending records in sync_inbox in idempotent batches.
// Kept named ProcessOutbox for backwards-compatible API endpoint invocation.
func (s *Service) ProcessOutbox(ctx context.Context, batchSize int) (int, error) {
	if s == nil || s.upstream == nil {
		return 0, ErrSyncUnavailable
	}

	if batchSize <= 0 {
		batchSize = 50
	}

	query := `
		SELECT id, source_record_id, source_type, idempotency_key, payload, processing_status, received_at, processed_at
		FROM public.sync_inbox
		WHERE processing_status = 'PENDING'
		ORDER BY received_at ASC
		LIMIT $1;
	`

	rows, err := s.db.Pool.Query(ctx, query, batchSize)
	if err != nil {
		return 0, fmt.Errorf("failed to query sync inbox: %w", err)
	}
	defer rows.Close()

	var records []database.SyncInbox
	for rows.Next() {
		var r database.SyncInbox
		if err := rows.Scan(
			&r.ID,
			&r.SourceRecordID,
			&r.SourceType,
			&r.IdempotencyKey,
			&r.Payload,
			&r.ProcessingStatus,
			&r.ReceivedAt,
			&r.ProcessedAt,
		); err != nil {
			return 0, fmt.Errorf("failed to scan sync inbox record: %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	if len(records) == 0 {
		return 0, nil
	}

	// Attempt upload to upstream using idempotency keys
	uploadErr := s.upstream.UploadBatch(ctx, records)
	if uploadErr != nil {
		// Never mark records PROCESSED on failure; they must remain PENDING.
		log.Printf("Sync inbox upload failed (records remain PENDING): %v", uploadErr)
		return 0, uploadErr
	}

	now := time.Now().UTC()

	// Update record statuses atomically in sync_inbox and update checkpoint ONLY upon true upload success
	err = s.db.WithTx(ctx, func(tx pgx.Tx) error {
		for _, rec := range records {
			updateQuery := `
				UPDATE public.sync_inbox
				SET processing_status = 'PROCESSED', processed_at = $2
				WHERE id = $1;
			`
			if _, err := tx.Exec(ctx, updateQuery, rec.ID, now); err != nil {
				return err
			}
		}

		if len(records) > 0 {
			checkpointQuery := `
				INSERT INTO public.sync_checkpoints (
					device_identifier, stream_name, last_sequence, last_successful_sync, updated_at
				)
				VALUES ('VENUE-LOCAL', 'SYNC_INBOX_UPLOAD', $1, $2, $2)
				ON CONFLICT (device_identifier, stream_name) DO UPDATE SET
					last_sequence = public.sync_checkpoints.last_sequence + EXCLUDED.last_sequence,
					last_successful_sync = EXCLUDED.last_successful_sync,
					updated_at = EXCLUDED.updated_at;
			`
			_, _ = tx.Exec(ctx, checkpointQuery, int64(len(records)), now)
		}

		return nil
	})

	if err != nil {
		return 0, fmt.Errorf("failed to update sync inbox statuses after upload: %w", err)
	}

	return len(records), nil
}

// IngestManifest downloads and stores authoritative event data, gates, and tickets.
func (s *Service) IngestManifest(ctx context.Context, pkg *ManifestPackage) error {
	if pkg == nil {
		return errors.New("manifest package cannot be nil")
	}

	// Verify event identity and required organizer ID
	if pkg.Event.ID == "" {
		return errors.New("cannot ingest manifest: event id is required")
	}
	if pkg.Event.OrganizerID == "" || pkg.Event.OrganizerID == "00000000-0000-0000-0000-000000000000" {
		return errors.New("cannot ingest manifest: event is missing a valid organizer_id")
	}

	// Verify tickets have required ownership references
	for _, t := range pkg.Tickets {
		if t.ID == "" {
			return errors.New("cannot ingest manifest: ticket id is required")
		}
		if t.OriginalOwnerID == "" || t.OriginalOwnerID == "00000000-0000-0000-0000-000000000000" {
			return fmt.Errorf("cannot ingest ticket %s: missing a valid original_owner_id", t.ID)
		}
		if t.CurrentOwnerID == "" || t.CurrentOwnerID == "00000000-0000-0000-0000-000000000000" {
			return fmt.Errorf("cannot ingest ticket %s: missing a valid current_owner_id", t.ID)
		}
	}

	// Verify package signature if provided
	if pkg.Signature != "" && !s.verifySignature(pkg.Event.ID, pkg.Signature) {
		return errors.New("invalid manifest package signature")
	}

	now := time.Now().UTC()

	return s.db.WithTx(ctx, func(tx pgx.Tx) error {
		// 1. Upsert events
		upsertEvent := `
			INSERT INTO public.events (
				id, organizer_id, event_name, description, venue_name,
				event_start, event_end, total_capacity, event_status, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (id) DO UPDATE SET
				organizer_id = EXCLUDED.organizer_id,
				event_name = EXCLUDED.event_name,
				description = EXCLUDED.description,
				venue_name = EXCLUDED.venue_name,
				event_start = EXCLUDED.event_start,
				event_end = EXCLUDED.event_end,
				total_capacity = EXCLUDED.total_capacity,
				event_status = EXCLUDED.event_status,
				updated_at = EXCLUDED.updated_at;
		`
		status := pkg.Event.EventStatus
		if status == "" {
			status = database.EventStatusPublished
		}

		_, err := tx.Exec(ctx, upsertEvent,
			pkg.Event.ID,
			pkg.Event.OrganizerID,
			pkg.Event.EventName,
			pkg.Event.Description,
			pkg.Event.VenueName,
			pkg.Event.EventStart,
			pkg.Event.EventEnd,
			pkg.Event.TotalCapacity,
			status,
			now,
			now,
		)
		if err != nil {
			return fmt.Errorf("failed to upsert event: %w", err)
		}

		// 2. Upsert venue_gates
		for _, gate := range pkg.Gates {
			upsertGate := `
				INSERT INTO public.venue_gates (
					id, event_id, device_identifier, gate_name, device_secret_hash, device_status, created_at
				)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
				ON CONFLICT (device_identifier) DO UPDATE SET
					event_id = EXCLUDED.event_id,
					gate_name = EXCLUDED.gate_name,
					device_secret_hash = EXCLUDED.device_secret_hash,
					device_status = EXCLUDED.device_status;
			`
			gateStatus := gate.DeviceStatus
			if gateStatus == "" {
				gateStatus = database.DeviceStatusActive
			}
			_, err = tx.Exec(ctx, upsertGate,
				gate.ID,
				pkg.Event.ID,
				gate.DeviceIdentifier,
				gate.GateName,
				gate.DeviceSecretHash,
				gateStatus,
				now,
			)
			if err != nil {
				return fmt.Errorf("failed to upsert venue gate '%s': %w", gate.DeviceIdentifier, err)
			}
		}

		// 3. Upsert device_credentials if provided
		for _, cred := range pkg.Credentials {
			upsertCred := `
				INSERT INTO public.device_credentials (
					id, device_identifier, gate_id, public_key, credential_signature,
					issuer_key_id, issued_at, expires_at, revoked_at, created_at
				)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
				ON CONFLICT (device_identifier) DO UPDATE SET
					gate_id = EXCLUDED.gate_id,
					public_key = EXCLUDED.public_key,
					credential_signature = EXCLUDED.credential_signature,
					issuer_key_id = EXCLUDED.issuer_key_id,
					expires_at = EXCLUDED.expires_at,
					revoked_at = EXCLUDED.revoked_at;
			`
			_, err = tx.Exec(ctx, upsertCred,
				cred.ID,
				cred.DeviceIdentifier,
				cred.GateID,
				cred.PublicKey,
				cred.CredentialSignature,
				cred.IssuerKeyID,
				cred.IssuedAt,
				cred.ExpiresAt,
				cred.RevokedAt,
				now,
			)
			if err != nil {
				return fmt.Errorf("failed to upsert device credential '%s': %w", cred.DeviceIdentifier, err)
			}
		}

		// 4. Ingest tickets into tickets table
		// Protect locally redeemed tickets: DO NOT overwrite if already USED.
		for _, t := range pkg.Tickets {
			upsertTicket := `
				INSERT INTO public.tickets (
					id, event_id, ticket_hash, original_owner_id, current_owner_id,
					ticket_type, price, ticket_status, valid_until, blockchain_token_id, created_at, updated_at
				)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
				ON CONFLICT (id) DO UPDATE SET
					ticket_hash = EXCLUDED.ticket_hash,
					original_owner_id = EXCLUDED.original_owner_id,
					current_owner_id = EXCLUDED.current_owner_id,
					ticket_type = EXCLUDED.ticket_type,
					price = EXCLUDED.price,
					ticket_status = EXCLUDED.ticket_status,
					valid_until = EXCLUDED.valid_until,
					updated_at = EXCLUDED.updated_at
				WHERE public.tickets.ticket_status != 'USED';
			`
			ticketType := t.TicketType
			if ticketType == "" {
				ticketType = "STANDARD"
			}
			ticketStatus := t.TicketStatus
			if ticketStatus == "" {
				ticketStatus = database.TicketStatusActive
			}

			_, err = tx.Exec(ctx, upsertTicket,
				t.ID,
				pkg.Event.ID,
				t.TicketHash,
				t.OriginalOwnerID,
				t.CurrentOwnerID,
				ticketType,
				t.Price,
				ticketStatus,
				t.ValidUntil,
				t.BlockchainTokenID,
				now,
				now,
			)
			if err != nil {
				return fmt.Errorf("failed to upsert ticket '%s': %w", t.ID, err)
			}
		}

		// 5. Insert manifest_metadata
		insertMeta := `
			INSERT INTO public.manifest_metadata (
				id, event_id, manifest_version, manifest_hash, signature, signer_key_id, issued_at, expires_at, created_at
			)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (event_id, manifest_version) DO UPDATE SET
				manifest_hash = EXCLUDED.manifest_hash,
				signature = EXCLUDED.signature,
				signer_key_id = EXCLUDED.signer_key_id,
				issued_at = EXCLUDED.issued_at,
				expires_at = EXCLUDED.expires_at;
		`
		var (
			signerKeyID     = "central-global-signer-1"
			issuedAt        = now
			expiresAt       = pkg.Event.EventEnd
			manifestSig     = pkg.Signature
			manifestHashHex string
		)

		if pkg.ManifestMetadata != nil {
			if pkg.ManifestMetadata.SignerKeyID != "" {
				signerKeyID = pkg.ManifestMetadata.SignerKeyID
			}
			if !pkg.ManifestMetadata.IssuedAt.IsZero() {
				issuedAt = pkg.ManifestMetadata.IssuedAt
			}
			if !pkg.ManifestMetadata.ExpiresAt.IsZero() {
				expiresAt = pkg.ManifestMetadata.ExpiresAt
			}
			if pkg.ManifestMetadata.Signature != "" {
				manifestSig = pkg.ManifestMetadata.Signature
			}
			manifestHashHex = pkg.ManifestMetadata.ManifestHash
		}

		if manifestHashHex == "" {
			manifestHash := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", pkg.Event.ID, pkg.ManifestVersion, len(pkg.Tickets))))
			manifestHashHex = hex.EncodeToString(manifestHash[:])
		}

		_, err = tx.Exec(ctx, insertMeta,
			pkg.Event.ID,
			pkg.ManifestVersion,
			manifestHashHex,
			manifestSig,
			signerKeyID,
			issuedAt,
			expiresAt,
			now,
		)
		if err != nil {
			return fmt.Errorf("failed to record manifest metadata: %w", err)
		}

		// 6. Update sync checkpoint
		upsertCheckpoint := `
			INSERT INTO public.sync_checkpoints (
				device_identifier, stream_name, last_sequence, last_successful_sync, updated_at
			)
			VALUES ('VENUE-LOCAL', 'MANIFEST_DOWNLOAD', $1, $2, $2)
			ON CONFLICT (device_identifier, stream_name) DO UPDATE SET
				last_sequence = EXCLUDED.last_sequence,
				last_successful_sync = EXCLUDED.last_successful_sync,
				updated_at = EXCLUDED.updated_at;
		`
		_, err = tx.Exec(ctx, upsertCheckpoint, pkg.ManifestVersion, now)
		if err != nil {
			return fmt.Errorf("failed to update sync checkpoint: %w", err)
		}

		return nil
	})
}

// IngestRevocations securely records ticket revocations and invalidates local active tickets.
func (s *Service) IngestRevocations(ctx context.Context, revocations []database.RevokedTicket) error {
	if len(revocations) == 0 {
		return nil
	}

	now := time.Now().UTC()

	return s.db.WithTx(ctx, func(tx pgx.Tx) error {
		var count int64 = 0

		for _, r := range revocations {
			// Record in revoked_tickets table
			insertRevoked := `
				INSERT INTO public.revoked_tickets (
					ticket_id, event_id, revocation_reason, revocation_signature, signer_key_id, revoked_at, created_at
				)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
				ON CONFLICT (ticket_id) DO NOTHING;
			`
			_, err := tx.Exec(ctx, insertRevoked,
				r.TicketID,
				r.EventID,
				r.RevocationReason,
				r.RevocationSignature,
				r.SignerKeyID,
				r.RevokedAt,
				now,
			)
			if err != nil {
				return fmt.Errorf("failed to insert revoked ticket: %w", err)
			}

			// Invalidate ticket in tickets table if currently active
			updateTickets := `
				UPDATE public.tickets
				SET ticket_status = 'CANCELLED', updated_at = $2
				WHERE (id::text = $1 OR ticket_hash = $1) AND ticket_status = 'ACTIVE';
			`
			_, err = tx.Exec(ctx, updateTickets, r.TicketID, now)
			if err != nil {
				return fmt.Errorf("failed to invalidate cancelled ticket: %w", err)
			}

			count++
		}

		// Update checkpoint
		upsertCheckpoint := `
			INSERT INTO public.sync_checkpoints (
				device_identifier, stream_name, last_sequence, last_successful_sync, updated_at
			)
			VALUES ('VENUE-LOCAL', 'REVOCATIONS', $1, $2, $2)
			ON CONFLICT (device_identifier, stream_name) DO UPDATE SET
				last_sequence = public.sync_checkpoints.last_sequence + EXCLUDED.last_sequence,
				last_successful_sync = EXCLUDED.last_successful_sync,
				updated_at = EXCLUDED.updated_at;
		`
		_, err := tx.Exec(ctx, upsertCheckpoint, count, now)
		if err != nil {
			return fmt.Errorf("failed to update revocation checkpoint: %w", err)
		}

		return nil
	})
}

// StartBackgroundSync launches the periodic sync inbox processor.
func (s *Service) StartBackgroundSync(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}

	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Println("Sync background worker stopped")
				return
			case <-ticker.C:
				if !s.IsUpstreamAvailable() {
					continue
				}
				processed, err := s.ProcessOutbox(ctx, 50)
				if err != nil {
					log.Printf("Background sync heartbeat: offline mode (%v)", err)
				} else if processed > 0 {
					log.Printf("Background sync: processed %d inbox records", processed)
				}
			}
		}
	}()
}

func (s *Service) verifySignature(eventID, providedSig string) bool {
	mac := hmac.New(sha256.New, []byte(s.secretKey))
	mac.Write([]byte(eventID + ":manifest"))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(providedSig))
}
