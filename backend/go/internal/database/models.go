package database

import (
	"encoding/json"
	"time"
)

// ScanResult represents the authoritative result of a ticket scan attempt.
type ScanResult string

const (
	ScanResultValid              ScanResult = "VALID"
	ScanResultDuplicate          ScanResult = "DUPLICATE"
	ScanResultInvalid            ScanResult = "INVALID"
	ScanResultExpired            ScanResult = "EXPIRED"
	ScanResultCancelled          ScanResult = "CANCELLED"
	ScanResultWrongGate          ScanResult = "WRONG_GATE"
	ScanResultRevoked            ScanResult = "REVOKED"
	ScanResultDeviceUnauthorized ScanResult = "DEVICE_UNAUTHORIZED"
)

// TicketStatus represents the state of a ticket in the tickets table.
// Constraints: ('ACTIVE', 'TRANSFERRED', 'USED', 'CANCELLED').
type TicketStatus string

const (
	TicketStatusActive      TicketStatus = "ACTIVE"
	TicketStatusTransferred TicketStatus = "TRANSFERRED"
	TicketStatusUsed        TicketStatus = "USED"
	TicketStatusCancelled   TicketStatus = "CANCELLED"
)

// DeviceStatus represents whether a physical scanning device is authorized.
// Constraints: ('ACTIVE', 'INACTIVE', 'REVOKED').
type DeviceStatus string

const (
	DeviceStatusActive   DeviceStatus = "ACTIVE"
	DeviceStatusInactive DeviceStatus = "INACTIVE"
	DeviceStatusRevoked  DeviceStatus = "REVOKED"
)

// SyncStatus represents the sync inbox processing status.
// Constraints: ('PENDING', 'PROCESSED', 'REJECTED').
type SyncStatus string

const (
	SyncStatusPending   SyncStatus = "PENDING"
	SyncStatusProcessed SyncStatus = "PROCESSED"
	SyncStatusRejected  SyncStatus = "REJECTED"
)

// EventStatus represents the event lifecycle status.
// Constraints: ('DRAFT', 'PUBLISHED', 'CANCELLED', 'COMPLETED').
type EventStatus string

const (
	EventStatusDraft     EventStatus = "DRAFT"
	EventStatusPublished EventStatus = "PUBLISHED"
	EventStatusCancelled EventStatus = "CANCELLED"
	EventStatusCompleted EventStatus = "COMPLETED"
)

// ============================================================================
// Authoritative 12-Table Schema Models (Mirrored from Neon PostgreSQL)
// ============================================================================

// 1. BlockchainTransaction maps to public.blockchain_transactions
type BlockchainTransaction struct {
	ID                string     `json:"id"`
	TicketID          *string    `json:"ticket_id,omitempty"`
	TransactionHash   *string    `json:"transaction_hash,omitempty"`
	BlockchainNetwork string     `json:"blockchain_network"`
	Operation         string     `json:"operation"`
	TransactionStatus string     `json:"transaction_status"`
	BlockNumber       *int64     `json:"block_number,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	ConfirmedAt       *time.Time `json:"confirmed_at,omitempty"`
}

// 2. DeviceCredential maps to public.device_credentials
type DeviceCredential struct {
	ID                  string     `json:"id"`
	DeviceIdentifier    string     `json:"device_identifier"`
	GateID              string     `json:"gate_id"`
	PublicKey           string     `json:"public_key"`
	CredentialSignature string     `json:"credential_signature"`
	IssuerKeyID         string     `json:"issuer_key_id"`
	IssuedAt            time.Time  `json:"issued_at"`
	ExpiresAt           time.Time  `json:"expires_at"`
	RevokedAt           *time.Time `json:"revoked_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
}

// 3. Event maps to public.events
type Event struct {
	ID            string      `json:"id"`
	OrganizerID   string      `json:"organizer_id"`
	EventName     string      `json:"event_name"`
	Description   *string     `json:"description,omitempty"`
	VenueName     *string     `json:"venue_name,omitempty"`
	EventStart    time.Time   `json:"event_start"`
	EventEnd      time.Time   `json:"event_end"`
	TotalCapacity int         `json:"total_capacity"`
	EventStatus   EventStatus `json:"event_status"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

// 4. ManifestMetadata maps to public.manifest_metadata
type ManifestMetadata struct {
	ID              string    `json:"id"`
	EventID         string    `json:"event_id"`
	ManifestVersion int64     `json:"manifest_version"`
	ManifestHash    string    `json:"manifest_hash"`
	Signature       string    `json:"signature"`
	SignerKeyID     string    `json:"signer_key_id"`
	IssuedAt        time.Time `json:"issued_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	CreatedAt       time.Time `json:"created_at"`
}

// 5. Redemption maps to public.redemptions
// Note: ticket_id has a UNIQUE constraint in the schema.
type Redemption struct {
	ID            string    `json:"id"`
	TicketID      string    `json:"ticket_id"`
	GateID        string    `json:"gate_id"`
	RedeemedAt    time.Time `json:"redeemed_at"`
	Source        string    `json:"source"` // 'ONLINE' or 'OFFLINE_SYNC'
	SyncReference *string   `json:"sync_reference,omitempty"`
}

// 6. RevokedTicket maps to public.revoked_tickets
// Note: ticket_id has a UNIQUE constraint in the schema.
type RevokedTicket struct {
	ID                  string    `json:"id"`
	TicketID            string    `json:"ticket_id"`
	EventID             string    `json:"event_id"`
	RevocationReason    *string   `json:"revocation_reason,omitempty"`
	RevocationSignature string    `json:"revocation_signature"`
	SignerKeyID         string    `json:"signer_key_id"`
	RevokedAt           time.Time `json:"revoked_at"`
	CreatedAt           time.Time `json:"created_at"`
}

// 7. SyncCheckpoint maps to public.sync_checkpoints
// Note: UNIQUE (device_identifier, stream_name).
type SyncCheckpoint struct {
	ID                 string     `json:"id"`
	DeviceIdentifier   string     `json:"device_identifier"`
	StreamName         string     `json:"stream_name"`
	LastSequence       int64      `json:"last_sequence"`
	LastSuccessfulSync *time.Time `json:"last_successful_sync,omitempty"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// 8. SyncInbox maps to public.sync_inbox
// Note: idempotency_key has a UNIQUE constraint.
type SyncInbox struct {
	ID               string          `json:"id"`
	SourceRecordID   string          `json:"source_record_id"`
	SourceType       string          `json:"source_type"`
	IdempotencyKey   string          `json:"idempotency_key"`
	Payload          json.RawMessage `json:"payload"`
	ProcessingStatus SyncStatus      `json:"processing_status"`
	ReceivedAt       time.Time       `json:"received_at"`
	ProcessedAt      *time.Time      `json:"processed_at,omitempty"`
}

// 9. TicketOwnership maps to public.ticket_ownership
type TicketOwnership struct {
	ID              string    `json:"id"`
	TicketID        string    `json:"ticket_id"`
	PreviousOwnerID *string   `json:"previous_owner_id,omitempty"`
	NewOwnerID      string    `json:"new_owner_id"`
	TransferType    string    `json:"transfer_type"`
	BlockchainTxID  *string   `json:"blockchain_tx_id,omitempty"`
	TransferredAt   time.Time `json:"transferred_at"`
}

// 10. Ticket maps to public.tickets
// Note: ticket_hash has a UNIQUE constraint.
type Ticket struct {
	ID                string       `json:"id"`
	EventID           string       `json:"event_id"`
	TicketHash        string       `json:"ticket_hash"`
	OriginalOwnerID   string       `json:"original_owner_id"`
	CurrentOwnerID    string       `json:"current_owner_id"`
	TicketType        string       `json:"ticket_type"`
	Price             float64      `json:"price"`
	TicketStatus      TicketStatus `json:"ticket_status"`
	ValidUntil        time.Time    `json:"valid_until"`
	BlockchainTokenID *string      `json:"blockchain_token_id,omitempty"`
	CreatedAt         time.Time    `json:"created_at"`
	UpdatedAt         time.Time    `json:"updated_at"`
}

// 11. User maps to public.users
type User struct {
	ID            string    `json:"id"`
	FullName      string    `json:"full_name"`
	Email         string    `json:"email"`
	PasswordHash  string    `json:"-"`
	Role          string    `json:"role"`
	WalletAddress *string   `json:"wallet_address,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// 12. VenueGate maps to public.venue_gates
// Note: device_identifier has a UNIQUE constraint.
type VenueGate struct {
	ID               string       `json:"id"`
	EventID          string       `json:"event_id"`
	DeviceIdentifier string       `json:"device_identifier"`
	GateName         string       `json:"gate_name"`
	DeviceSecretHash string       `json:"-"`
	DeviceStatus     DeviceStatus `json:"device_status"`
	CreatedAt        time.Time    `json:"created_at"`
}

// ============================================================================
// Scan API Request & Response Contracts
// ============================================================================

// ScanRequest represents the incoming payload from a gate scanning device.
type ScanRequest struct {
	DeviceIdentifier string `json:"device_identifier"`
	DeviceSecret     string `json:"device_secret"`
	TicketID         string `json:"ticket_id,omitempty"`
	QRToken          string `json:"qr_token,omitempty"`
	TicketHash       string `json:"ticket_hash,omitempty"`
	Signature        string `json:"signature,omitempty"`
}

// ScanResponse represents the response returned to the gate scanning device.
type ScanResponse struct {
	Result    ScanResult `json:"result"`
	Accepted  bool       `json:"accepted"`
	Message   string     `json:"message"`
	TicketID  string     `json:"ticket_id,omitempty"`
	EventID   string     `json:"event_id,omitempty"`
	GateID    string     `json:"gate_id,omitempty"`
	GateCode  string     `json:"gate_code,omitempty"`
	GateName  string     `json:"gate_name,omitempty"`
	ScannedAt time.Time  `json:"scanned_at"`
	ScanLogID string     `json:"scan_log_id,omitempty"`
}
