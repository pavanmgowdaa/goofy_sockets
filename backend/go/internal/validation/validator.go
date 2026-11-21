package validation

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"fairpass/internal/auth"
	"fairpass/internal/database"

	"github.com/jackc/pgx/v5"
)

// Repository defines data access operations required for ticket validation.
type Repository interface {
	GetEvent(ctx context.Context, eventID string) (*database.Event, error)
	GetManifestMetadata(ctx context.Context, eventID string) (*database.ManifestMetadata, error)
	IsTicketRevoked(ctx context.Context, lookupKey, eventID string) (bool, error)
	ExecuteScanTransaction(
		ctx context.Context,
		lookupKey string,
		authDev *auth.AuthorizedDeviceContext,
		now time.Time,
	) (*database.ScanResponse, error)
	RecordRejection(
		ctx context.Context,
		ticketID string,
		authDev *auth.AuthorizedDeviceContext,
		result database.ScanResult,
		message string,
		scannedAt time.Time,
	) (string, error)
}

// Validator orchestrates the complete 12-step offline ticket validation workflow.
type Validator struct {
	repo          Repository
	authenticator auth.Authenticator
	secretKey     string
}

// NewValidator creates a Validator instance backed by PostgreSQL.
func NewValidator(db *database.DB, authenticator auth.Authenticator, secretKey string) *Validator {
	if secretKey == "" {
		secretKey = "fairpass-venue-offline-secret-key"
	}
	return &Validator{
		repo:          NewSQLRepository(db),
		authenticator: authenticator,
		secretKey:     secretKey,
	}
}

// NewValidatorWithRepo creates a Validator instance with a custom or mock repository.
func NewValidatorWithRepo(repo Repository, authenticator auth.Authenticator, secretKey string) *Validator {
	if secretKey == "" {
		secretKey = "fairpass-venue-offline-secret-key"
	}
	return &Validator{
		repo:          repo,
		authenticator: authenticator,
		secretKey:     secretKey,
	}
}

// Validate processes a scan request following the strict verification protocol.
func (v *Validator) Validate(ctx context.Context, req database.ScanRequest) (*database.ScanResponse, error) {
	now := time.Now().UTC()

	// ------------------------------------------------------------------------
	// STEP 1, 2, 3: Authenticate Device, Check Status, Identify Gate Assignment
	// ------------------------------------------------------------------------
	authDev, err := v.authenticator.Authenticate(ctx, req.DeviceIdentifier, req.DeviceSecret)
	if err != nil {
		log.Printf("Scan rejection: device unauthorized for '%s': %v", req.DeviceIdentifier, err)
		return &database.ScanResponse{
			Result:    database.ScanResultDeviceUnauthorized,
			Accepted:  false,
			Message:   "Scanning device is not authorized or has no active gate assignment",
			TicketID:  req.TicketID,
			ScannedAt: now,
		}, nil
	}

	// ------------------------------------------------------------------------
	// STEP 4: Resolve QR Credential / Ticket Hash
	// ------------------------------------------------------------------------
	lookupKey := req.TicketID
	isGlobalQRToken := req.QRToken != ""
	if isGlobalQRToken {
		hash, hashErr := HashGlobalQRToken(req.QRToken)
		if hashErr != nil {
			return v.recordRejection(ctx, "", authDev, database.ScanResultInvalid, "QR token is malformed", now)
		}
		lookupKey = hash
	} else if req.TicketHash != "" {
		lookupKey = req.TicketHash
	} else if req.Signature != "" {
		validSig := v.VerifySignature(req.TicketID, authDev.EventID, req.Signature)
		if !validSig {
			return v.recordRejection(ctx, req.TicketID, authDev, database.ScanResultInvalid, "Invalid ticket cryptographic signature", now)
		}
	}
	if lookupKey == "" {
		return v.recordRejection(ctx, "", authDev, database.ScanResultInvalid, "qr_token, ticket_hash or ticket_id is required", now)
	}

	// ------------------------------------------------------------------------
	// STEP 5: Check Event Validity in events Table
	// ------------------------------------------------------------------------
	event, err := v.repo.GetEvent(ctx, authDev.EventID)
	if err != nil {
		return v.recordRejection(ctx, req.TicketID, authDev, database.ScanResultInvalid, "Event does not exist in local database", now)
	}

	if event.EventStatus == database.EventStatusCancelled {
		return v.recordRejection(ctx, req.TicketID, authDev, database.ScanResultCancelled, "Event has been cancelled", now)
	}
	if now.Before(event.EventStart) {
		return v.recordRejection(ctx, req.TicketID, authDev, database.ScanResultInvalid, "Event has not started yet", now)
	}
	if now.After(event.EventEnd) {
		return v.recordRejection(ctx, req.TicketID, authDev, database.ScanResultExpired, "Event has already ended", now)
	}

	// ------------------------------------------------------------------------
	// STEP 6: Check Manifest Metadata Validity & Expiry
	// ------------------------------------------------------------------------
	manifestMeta, err := v.repo.GetManifestMetadata(ctx, authDev.EventID)
	if err != nil || manifestMeta == nil {
		return v.recordRejection(ctx, req.TicketID, authDev, database.ScanResultInvalid, "No valid manifest metadata found for event", now)
	}
	if now.After(manifestMeta.ExpiresAt) {
		return v.recordRejection(ctx, req.TicketID, authDev, database.ScanResultExpired, "Manifest validity expired", now)
	}
	if now.Before(manifestMeta.IssuedAt) {
		return v.recordRejection(ctx, req.TicketID, authDev, database.ScanResultInvalid, "Manifest is not yet valid", now)
	}

	// ------------------------------------------------------------------------
	// STEP 7: Check Revoked Tickets in revoked_tickets Table
	// ------------------------------------------------------------------------
	revoked, err := v.repo.IsTicketRevoked(ctx, lookupKey, authDev.EventID)
	if err != nil {
		return nil, fmt.Errorf("failed to query revocation status: %w", err)
	}
	if revoked {
		return v.recordRejection(ctx, req.TicketID, authDev, database.ScanResultRevoked, "Ticket has been revoked or refunded", now)
	}

	// ------------------------------------------------------------------------
	// STEP 8-12: Atomic Duplicate Prevention & Entry Recording in redemptions
	// ------------------------------------------------------------------------
	resp, err := v.repo.ExecuteScanTransaction(ctx, lookupKey, authDev, now)
	if err != nil {
		return nil, fmt.Errorf("validation transaction failure: %w", err)
	}

	// If rejected inside transaction (e.g. DUPLICATE, CANCELLED, WRONG_GATE), record rejection
	if resp != nil && !resp.Accepted {
		rejectionTicketID := resp.TicketID
		if rejectionTicketID == "" && !isGlobalQRToken {
			rejectionTicketID = req.TicketID
		}
		return v.recordRejection(ctx, rejectionTicketID, authDev, resp.Result, resp.Message, now)
	}

	return resp, nil
}

func (v *Validator) recordRejection(
	ctx context.Context,
	ticketID string,
	authDev *auth.AuthorizedDeviceContext,
	result database.ScanResult,
	message string,
	scannedAt time.Time,
) (*database.ScanResponse, error) {
	scanLogID, err := v.repo.RecordRejection(ctx, ticketID, authDev, result, message, scannedAt)
	if err != nil {
		log.Printf("Warning: failed to record rejected scan: %v", err)
	}

	return &database.ScanResponse{
		Result:    result,
		Accepted:  false,
		Message:   message,
		TicketID:  ticketID,
		EventID:   authDev.EventID,
		GateID:    authDev.EventGateID,
		GateCode:  authDev.GateCode,
		GateName:  authDev.GateName,
		ScannedAt: scannedAt,
		ScanLogID: scanLogID,
	}, nil
}

// HashGlobalQRToken validates and hashes the 32-byte hex bearer token.
func HashGlobalQRToken(token string) (string, error) {
	if len(token) != 64 {
		return "", errors.New("QR token must be a 32-byte hex value")
	}
	if _, err := hex.DecodeString(token); err != nil {
		return "", errors.New("QR token must be hexadecimal")
	}
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:]), nil
}

// ComputeSignature calculates the HMAC-SHA256 signature for ticket verification.
func (v *Validator) ComputeSignature(ticketID, eventID string) string {
	mac := hmac.New(sha256.New, []byte(v.secretKey))
	mac.Write([]byte(ticketID + ":" + eventID))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature verifies the HMAC signature of a ticket.
func (v *Validator) VerifySignature(ticketID, eventID, providedSig string) bool {
	expectedSig := v.ComputeSignature(ticketID, eventID)
	return hmac.Equal([]byte(expectedSig), []byte(providedSig))
}

// ============================================================================
// SQLRepository: PostgreSQL 12-Table Implementation
// ============================================================================

type SQLRepository struct {
	db *database.DB
}

func NewSQLRepository(db *database.DB) *SQLRepository {
	return &SQLRepository{db: db}
}

func (r *SQLRepository) GetEvent(ctx context.Context, eventID string) (*database.Event, error) {
	query := `
		SELECT id, organizer_id, event_name, description, venue_name, event_start, event_end, total_capacity, event_status
		FROM public.events
		WHERE id = $1;
	`
	var e database.Event
	err := r.db.Pool.QueryRow(ctx, query, eventID).Scan(
		&e.ID,
		&e.OrganizerID,
		&e.EventName,
		&e.Description,
		&e.VenueName,
		&e.EventStart,
		&e.EventEnd,
		&e.TotalCapacity,
		&e.EventStatus,
	)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (r *SQLRepository) GetManifestMetadata(ctx context.Context, eventID string) (*database.ManifestMetadata, error) {
	query := `
		SELECT id, event_id, manifest_version, manifest_hash, signature, signer_key_id, issued_at, expires_at, created_at
		FROM public.manifest_metadata
		WHERE event_id = $1::text
		ORDER BY manifest_version DESC
		LIMIT 1;
	`
	var m database.ManifestMetadata
	err := r.db.Pool.QueryRow(ctx, query, eventID).Scan(
		&m.ID,
		&m.EventID,
		&m.ManifestVersion,
		&m.ManifestHash,
		&m.Signature,
		&m.SignerKeyID,
		&m.IssuedAt,
		&m.ExpiresAt,
		&m.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *SQLRepository) IsTicketRevoked(ctx context.Context, lookupKey, eventID string) (bool, error) {
	var count int
	query := `
		SELECT COUNT(1)
		FROM public.revoked_tickets rt
		WHERE rt.event_id = $2::text
		  AND (
			  rt.ticket_id = $1
			  OR rt.ticket_id = (
				  SELECT t.id::text FROM public.tickets t WHERE t.ticket_hash = $1
			  )
		  );
	`
	err := r.db.Pool.QueryRow(ctx, query, lookupKey, eventID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *SQLRepository) ExecuteScanTransaction(
	ctx context.Context,
	lookupKey string,
	authDev *auth.AuthorizedDeviceContext,
	now time.Time,
) (*database.ScanResponse, error) {
	var response *database.ScanResponse

	err := r.db.WithTx(ctx, func(tx pgx.Tx) error {
		// 1. Lock and retrieve ticket from public.tickets
		ticketQuery := `
			SELECT 
				id, event_id, ticket_hash, original_owner_id, current_owner_id,
				ticket_type, price, ticket_status, valid_until
			FROM public.tickets
			WHERE id::text = $1 OR ticket_hash = $1
			FOR UPDATE;
		`

		var t database.Ticket
		err := tx.QueryRow(ctx, ticketQuery, lookupKey).Scan(
			&t.ID,
			&t.EventID,
			&t.TicketHash,
			&t.OriginalOwnerID,
			&t.CurrentOwnerID,
			&t.TicketType,
			&t.Price,
			&t.TicketStatus,
			&t.ValidUntil,
		)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				response = &database.ScanResponse{
					Result:   database.ScanResultInvalid,
					Accepted: false,
					TicketID: lookupKey,
					Message:  "Ticket not found in local database",
				}
				return nil
			}
			return err
		}

		if now.After(t.ValidUntil) {
			response = &database.ScanResponse{
				Result:   database.ScanResultExpired,
				Accepted: false,
				TicketID: t.ID,
				Message:  "Ticket expired",
			}
			return nil
		}

		if t.EventID != authDev.EventID {
			response = &database.ScanResponse{
				Result:   database.ScanResultWrongGate,
				Accepted: false,
				TicketID: t.ID,
				Message:  "Ticket does not belong to this event",
			}
			return nil
		}

		if t.TicketStatus == database.TicketStatusCancelled {
			response = &database.ScanResponse{
				Result:   database.ScanResultCancelled,
				Accepted: false,
				TicketID: t.ID,
				Message:  "Ticket has been cancelled",
			}
			return nil
		}
		if t.TicketStatus == database.TicketStatusUsed {
			response = &database.ScanResponse{
				Result:   database.ScanResultDuplicate,
				Accepted: false,
				TicketID: t.ID,
				Message:  "Ticket has already been used",
			}
			return nil
		}
		if t.TicketStatus != database.TicketStatusActive {
			response = &database.ScanResponse{
				Result:   database.ScanResultInvalid,
				Accepted: false,
				TicketID: t.ID,
				Message:  fmt.Sprintf("Invalid ticket status: %s", t.TicketStatus),
			}
			return nil
		}

		// 2. Check if already redeemed in public.redemptions
		var redemptionCount int
		err = tx.QueryRow(ctx, `
			SELECT COUNT(1) 
			FROM public.redemptions 
			WHERE ticket_id = $1;
		`, t.ID).Scan(&redemptionCount)
		if err != nil {
			return err
		}
		if redemptionCount > 0 {
			response = &database.ScanResponse{
				Result:   database.ScanResultDuplicate,
				Accepted: false,
				TicketID: t.ID,
				Message:  "Ticket has already been redeemed",
			}
			return nil
		}

		// 3. Atomically record accepted entry in public.redemptions
		// redemptions.ticket_id has a UNIQUE constraint.
		var redemptionID string
		syncRef := generateUUID()
		insertRedemption := `
			INSERT INTO public.redemptions (
				ticket_id, gate_id, redeemed_at, source, sync_reference
			)
			VALUES ($1, $2, $3, 'OFFLINE_SYNC', $4)
			RETURNING id;
		`
		err = tx.QueryRow(ctx, insertRedemption,
			t.ID,
			authDev.EventGateID,
			now,
			syncRef,
		).Scan(&redemptionID)
		if err != nil {
			if database.IsUniqueViolation(err) {
				response = &database.ScanResponse{
					Result:   database.ScanResultDuplicate,
					Accepted: false,
					TicketID: t.ID,
					Message:  "Concurrent scan detected: ticket already redeemed",
				}
				return nil
			}
			return err
		}

		// 4. Queue accepted redemption into sync_inbox with idempotency key
		idempotencyKey := fmt.Sprintf("redemption:%s", t.ID)
		inboxPayload, err := json.Marshal(map[string]any{
			"redemption_id":     redemptionID,
			"ticket_id":         t.ID,
			"gate_id":           authDev.EventGateID,
			"device_identifier": authDev.DeviceIdentifier,
			"gate_name":         authDev.GateName,
			"redeemed_at":       now.Format(time.RFC3339Nano),
			"sync_reference":    syncRef,
			"source":            "OFFLINE_SYNC",
		})
		if err != nil {
			return err
		}

		insertInbox := `
			INSERT INTO public.sync_inbox (
				source_record_id, source_type, idempotency_key, payload, processing_status, received_at
			)
			VALUES ($1, 'REDEMPTION', $2, $3, 'PENDING', $4)
			ON CONFLICT (idempotency_key) DO NOTHING;
		`
		if _, err = tx.Exec(ctx, insertInbox, redemptionID, idempotencyKey, inboxPayload, now); err != nil {
			return err
		}

		// Important: Do not update global ticket status to USED from local scanner.
		// Local offline acceptance is recorded locally in redemptions and sync_inbox.

		response = &database.ScanResponse{
			Result:    database.ScanResultValid,
			Accepted:  true,
			Message:   "Ticket verified and entry granted",
			TicketID:  t.ID,
			EventID:   authDev.EventID,
			GateID:    authDev.EventGateID,
			GateCode:  authDev.GateCode,
			GateName:  authDev.GateName,
			ScannedAt: now,
			ScanLogID: redemptionID,
		}

		return nil
	})

	return response, err
}

func (r *SQLRepository) RecordRejection(
	ctx context.Context,
	ticketID string,
	authDev *auth.AuthorizedDeviceContext,
	result database.ScanResult,
	message string,
	scannedAt time.Time,
) (string, error) {
	rejectionID := generateUUID()

	err := r.db.WithTx(ctx, func(tx pgx.Tx) error {
		idempotencyKey := fmt.Sprintf("rejection:%s:%d", rejectionID, scannedAt.UnixNano())
		payload, err := json.Marshal(map[string]any{
			"ticket_id":         ticketID,
			"device_identifier": authDev.DeviceIdentifier,
			"event_id":          authDev.EventID,
			"gate_id":           authDev.EventGateID,
			"gate_name":         authDev.GateName,
			"result":            result,
			"reason":            message,
			"scanned_at":        scannedAt.Format(time.RFC3339Nano),
		})
		if err != nil {
			return err
		}

		insertInbox := `
			INSERT INTO public.sync_inbox (
				source_record_id, source_type, idempotency_key, payload, processing_status, received_at
			)
			VALUES ($1, 'SCAN_REJECTION', $2, $3, 'PENDING', $4)
			ON CONFLICT (idempotency_key) DO NOTHING;
		`
		_, err = tx.Exec(ctx, insertInbox, rejectionID, idempotencyKey, payload, scannedAt)
		return err
	})

	return rejectionID, err
}

func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
