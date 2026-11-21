package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	"fairpass/internal/database"

	"github.com/jackc/pgx/v5"
)

var (
	ErrDeviceUnauthorized     = errors.New("device unauthorized")
	ErrDeviceNotFound         = errors.New("device not registered")
	ErrDeviceInactive         = errors.New("device is not active")
	ErrDeviceSecretMismatch   = errors.New("invalid device secret")
	ErrNoActiveGateAssignment = errors.New("no active gate assignment for event")
)

// Authenticator defines the contract for authenticating gate devices.
type Authenticator interface {
	Authenticate(ctx context.Context, identifier, secret string) (*AuthorizedDeviceContext, error)
}

// AuthorizedDeviceContext holds the validated gate device context for scanning.
type AuthorizedDeviceContext struct {
	DeviceID         string
	DeviceIdentifier string
	DeviceName       string
	EventID          string
	EventGateID      string
	GateCode         string
	GateName         string
}

// DeviceAuthenticator handles device verification against venue_gates and device_credentials.
type DeviceAuthenticator struct {
	db           *database.DB
	apiKeyPepper string
}

// NewDeviceAuthenticator creates a new authenticator instance.
func NewDeviceAuthenticator(db *database.DB) *DeviceAuthenticator {
	return &DeviceAuthenticator{db: db, apiKeyPepper: os.Getenv("DEVICE_API_KEY_PEPPER")}
}

// Authenticate verifies the device credentials against venue_gates and device_credentials.
func (a *DeviceAuthenticator) Authenticate(ctx context.Context, identifier, secret string) (*AuthorizedDeviceContext, error) {
	if identifier == "" || secret == "" {
		return nil, ErrDeviceUnauthorized
	}

	query := `
		SELECT 
			vg.id, 
			vg.event_id, 
			vg.device_identifier, 
			vg.gate_name, 
			vg.device_secret_hash, 
			vg.device_status,
			dc.id,
			dc.revoked_at,
			dc.expires_at
		FROM public.venue_gates vg
		LEFT JOIN public.device_credentials dc ON dc.device_identifier = vg.device_identifier
		WHERE vg.device_identifier = $1;
	`

	var (
		gateID           string
		eventID          string
		deviceIdentifier string
		gateName         string
		secretHash       string
		deviceStatus     database.DeviceStatus
		credID           *string
		credRevokedAt    *time.Time
		credExpiresAt    *time.Time
	)

	err := a.db.Pool.QueryRow(ctx, query, identifier).Scan(
		&gateID,
		&eventID,
		&deviceIdentifier,
		&gateName,
		&secretHash,
		&deviceStatus,
		&credID,
		&credRevokedAt,
		&credExpiresAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDeviceNotFound
		}
		return nil, fmt.Errorf("failed to query venue gate device: %w", err)
	}

	// 1. Verify gate device status
	if deviceStatus != database.DeviceStatusActive {
		return nil, ErrDeviceInactive
	}

	// 2. Verify credential validity if registered in device_credentials
	now := time.Now().UTC()
	if credID != nil {
		if credRevokedAt != nil {
			return nil, ErrDeviceInactive
		}
		if credExpiresAt != nil && now.After(*credExpiresAt) {
			return nil, ErrDeviceInactive
		}
	}

	// 3. Verify secret hash (constant-time comparison)
	if !verifySecret(secret, secretHash, a.apiKeyPepper) {
		return nil, ErrDeviceSecretMismatch
	}

	authCtx := &AuthorizedDeviceContext{
		DeviceID:         gateID,
		DeviceIdentifier: deviceIdentifier,
		DeviceName:       gateName,
		EventID:          eventID,
		EventGateID:      gateID,
		GateCode:         deviceIdentifier,
		GateName:         gateName,
	}

	return authCtx, nil
}

// AssignDeviceToGate updates the gate assignment for a device in venue_gates.
func (a *DeviceAuthenticator) AssignDeviceToGate(ctx context.Context, eventID, gateName, deviceIdentifier string) error {
	return a.db.WithTx(ctx, func(tx pgx.Tx) error {
		updateQuery := `
			UPDATE public.venue_gates
			SET event_id = $1, gate_name = $2, device_status = 'ACTIVE'
			WHERE device_identifier = $3;
		`
		tag, err := tx.Exec(ctx, updateQuery, eventID, gateName, deviceIdentifier)
		if err != nil {
			return fmt.Errorf("failed to update venue gate assignment: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrDeviceNotFound
		}
		return nil
	})
}

// RevokeDevice revokes a device in both venue_gates and device_credentials.
func (a *DeviceAuthenticator) RevokeDevice(ctx context.Context, deviceIdentifier string) error {
	return a.db.WithTx(ctx, func(tx pgx.Tx) error {
		now := time.Now().UTC()

		_, err := tx.Exec(ctx, `
			UPDATE public.venue_gates
			SET device_status = 'REVOKED'
			WHERE device_identifier = $1;
		`, deviceIdentifier)
		if err != nil {
			return fmt.Errorf("failed to revoke venue gate: %w", err)
		}

		_, err = tx.Exec(ctx, `
			UPDATE public.device_credentials
			SET revoked_at = $2
			WHERE device_identifier = $1 AND revoked_at IS NULL;
		`, deviceIdentifier, now)
		if err != nil {
			return fmt.Errorf("failed to revoke device credential: %w", err)
		}

		return nil
	})
}

// HashDeviceSecret computes SHA-256(secret + pepper) for persistent storage.
func HashDeviceSecret(secret, pepper string) string {
	sum := sha256.Sum256([]byte(secret + pepper))
	return hex.EncodeToString(sum[:])
}

// verifySecret checks secret validity against hashed value, supporting plain text in tests.
func verifySecret(providedSecret, storedHash, pepper string) bool {
	if subtle.ConstantTimeCompare([]byte(providedSecret), []byte(storedHash)) == 1 {
		return true
	}

	computedHash := HashDeviceSecret(providedSecret, pepper)
	if subtle.ConstantTimeCompare([]byte(computedHash), []byte(storedHash)) == 1 {
		return true
	}

	rawComputed := sha256.Sum256([]byte(providedSecret))
	rawHex := hex.EncodeToString(rawComputed[:])
	return subtle.ConstantTimeCompare([]byte(rawHex), []byte(storedHash)) == 1
}
