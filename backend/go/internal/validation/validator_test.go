package validation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fairpass/internal/auth"
	"fairpass/internal/database"
)

// MockAuthenticator implements auth.Authenticator for testing.
type MockAuthenticator struct {
	mu           sync.RWMutex
	devices      map[string]*database.VenueGate
	credentials  map[string]*database.DeviceCredential
	assignments  map[string]*auth.AuthorizedDeviceContext
	failAuth     bool
	unauthorized bool
}

func NewMockAuthenticator() *MockAuthenticator {
	return &MockAuthenticator{
		devices:     make(map[string]*database.VenueGate),
		credentials: make(map[string]*database.DeviceCredential),
		assignments: make(map[string]*auth.AuthorizedDeviceContext),
	}
}

func (m *MockAuthenticator) Authenticate(ctx context.Context, identifier, secret string) (*auth.AuthorizedDeviceContext, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.failAuth || m.unauthorized {
		return nil, auth.ErrDeviceUnauthorized
	}

	dev, ok := m.devices[identifier]
	if !ok || dev.DeviceStatus != database.DeviceStatusActive {
		return nil, auth.ErrDeviceInactive
	}

	if cred, ok := m.credentials[identifier]; ok && cred != nil {
		now := time.Now().UTC()
		if cred.RevokedAt != nil {
			return nil, auth.ErrDeviceInactive
		}
		if !cred.ExpiresAt.IsZero() && now.After(cred.ExpiresAt) {
			return nil, auth.ErrDeviceInactive
		}
	}

	if dev.DeviceSecretHash != secret {
		return nil, auth.ErrDeviceSecretMismatch
	}

	assign, ok := m.assignments[dev.ID]
	if !ok {
		return nil, auth.ErrNoActiveGateAssignment
	}

	return assign, nil
}

// MockRepository implements Repository for fast, deterministic unit & concurrent testing.
type MockRepository struct {
	mu          sync.Mutex
	events      map[string]*database.Event
	metadata    map[string]*database.ManifestMetadata
	tickets     map[string]*database.Ticket
	revoked     map[string]bool
	redemptions map[string]*database.Redemption
	inbox       []database.SyncInbox
}

func NewMockRepository() *MockRepository {
	return &MockRepository{
		events:      make(map[string]*database.Event),
		metadata:    make(map[string]*database.ManifestMetadata),
		tickets:     make(map[string]*database.Ticket),
		revoked:     make(map[string]bool),
		redemptions: make(map[string]*database.Redemption),
	}
}

func (m *MockRepository) GetEvent(ctx context.Context, eventID string) (*database.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.events[eventID]
	if !ok {
		return nil, fmt.Errorf("event not found")
	}
	return e, nil
}

func (m *MockRepository) GetManifestMetadata(ctx context.Context, eventID string) (*database.ManifestMetadata, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	meta, ok := m.metadata[eventID]
	if !ok {
		return nil, fmt.Errorf("metadata not found")
	}
	return meta, nil
}

func (m *MockRepository) IsTicketRevoked(ctx context.Context, lookupKey, eventID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.revoked[lookupKey+":"+eventID], nil
}

func (m *MockRepository) ExecuteScanTransaction(
	ctx context.Context,
	lookupKey string,
	authDev *auth.AuthorizedDeviceContext,
	now time.Time,
) (*database.ScanResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tickets[lookupKey]
	if !ok {
		// Try lookup by ticket_hash
		for _, ticket := range m.tickets {
			if ticket.TicketHash == lookupKey {
				t = ticket
				ok = true
				break
			}
		}
	}
	if !ok {
		return &database.ScanResponse{
			Result:   database.ScanResultInvalid,
			Accepted: false,
			Message:  "Ticket not found in local database",
		}, nil
	}

	if now.After(t.ValidUntil) {
		return &database.ScanResponse{
			Result:   database.ScanResultExpired,
			Accepted: false,
			Message:  "Ticket expired",
		}, nil
	}

	if t.EventID != authDev.EventID {
		return &database.ScanResponse{
			Result:   database.ScanResultWrongGate,
			Accepted: false,
			Message:  "Ticket does not belong to this event",
		}, nil
	}

	if t.TicketStatus == database.TicketStatusCancelled {
		return &database.ScanResponse{
			Result:   database.ScanResultCancelled,
			Accepted: false,
			Message:  "Ticket has been cancelled",
		}, nil
	}

	if t.TicketStatus == database.TicketStatusUsed || m.redemptions[t.ID] != nil {
		return &database.ScanResponse{
			Result:   database.ScanResultDuplicate,
			Accepted: false,
			Message:  "Ticket has already been redeemed",
		}, nil
	}

	// Atomically record redemption in redemptions table
	redemptionID := fmt.Sprintf("redempt-%d", len(m.redemptions)+1)
	m.redemptions[t.ID] = &database.Redemption{
		ID:         redemptionID,
		TicketID:   t.ID,
		GateID:     authDev.EventGateID,
		RedeemedAt: now,
		Source:     "OFFLINE_SYNC",
	}

	payload, _ := json.Marshal(map[string]any{
		"redemption_id":     redemptionID,
		"ticket_id":         t.ID,
		"gate_id":           authDev.EventGateID,
		"device_identifier": authDev.DeviceIdentifier,
		"gate_name":         authDev.GateName,
		"redeemed_at":       now.Format(time.RFC3339Nano),
		"source":            "OFFLINE_SYNC",
	})

	m.inbox = append(m.inbox, database.SyncInbox{
		ID:               fmt.Sprintf("inbox-%d", len(m.inbox)+1),
		SourceRecordID:   redemptionID,
		SourceType:       "REDEMPTION",
		IdempotencyKey:   fmt.Sprintf("redemption:%s", t.ID),
		Payload:          payload,
		ProcessingStatus: database.SyncStatusPending,
		ReceivedAt:       now,
	})

	return &database.ScanResponse{
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
	}, nil
}

func (m *MockRepository) RecordRejection(
	ctx context.Context,
	ticketID string,
	authDev *auth.AuthorizedDeviceContext,
	result database.ScanResult,
	message string,
	scannedAt time.Time,
) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rejectionID := fmt.Sprintf("reject-%d", len(m.inbox)+1)
	m.inbox = append(m.inbox, database.SyncInbox{
		ID:               fmt.Sprintf("inbox-%d", len(m.inbox)+1),
		SourceRecordID:   rejectionID,
		SourceType:       "SCAN_REJECTION",
		IdempotencyKey:   fmt.Sprintf("rejection:%s:%d", rejectionID, scannedAt.UnixNano()),
		ProcessingStatus: database.SyncStatusPending,
		ReceivedAt:       scannedAt,
	})

	return rejectionID, nil
}

// setupFixture sets up a standard event, gates, device, and ticket using the 12-table models.
func setupFixture() (*Validator, *MockRepository, *MockAuthenticator, string, string, string, string) {
	eventID := "11111111-1111-1111-1111-111111111111"
	gateAID := "22222222-2222-2222-2222-222222222222"
	gateBID := "33333333-3333-3333-3333-333333333333"
	ticketID := "55555555-5555-5555-5555-555555555555"

	repo := NewMockRepository()
	authMock := NewMockAuthenticator()

	now := time.Now().UTC()
	repo.events[eventID] = &database.Event{
		ID:          eventID,
		EventName:   "FAIRPASS Champions Cup",
		EventStart:  now.Add(-2 * time.Hour),
		EventEnd:    now.Add(6 * time.Hour),
		EventStatus: database.EventStatusPublished,
	}

	repo.metadata[eventID] = &database.ManifestMetadata{
		EventID:         eventID,
		ManifestVersion: 1,
		ManifestHash:    "hash-meta-001",
		Signature:       "sig-meta-001",
		SignerKeyID:     "signer-001",
		IssuedAt:        now.Add(-2 * time.Hour),
		ExpiresAt:       now.Add(6 * time.Hour),
	}

	authMock.devices["DEV-01"] = &database.VenueGate{
		ID:               gateAID,
		EventID:          eventID,
		DeviceIdentifier: "DEV-01",
		GateName:         "North Turnstile A",
		DeviceSecretHash: "secret123",
		DeviceStatus:     database.DeviceStatusActive,
	}

	authMock.assignments[gateAID] = &auth.AuthorizedDeviceContext{
		DeviceID:         gateAID,
		DeviceIdentifier: "DEV-01",
		EventID:          eventID,
		EventGateID:      gateAID,
		GateCode:         "DEV-01",
		GateName:         "North Turnstile A",
	}

	repo.tickets[ticketID] = &database.Ticket{
		ID:           ticketID,
		EventID:      eventID,
		TicketHash:   "hash-ticket-001",
		TicketType:   "VIP",
		TicketStatus: database.TicketStatusActive,
		ValidUntil:   now.Add(4 * time.Hour),
	}

	val := NewValidatorWithRepo(repo, authMock, "test-secret")
	return val, repo, authMock, eventID, gateAID, gateBID, ticketID
}

// TestRequirement1_ValidTicketScannedAtGate
func TestRequirement1_ValidTicketScannedAtGate(t *testing.T) {
	val, repo, _, eventID, _, _, ticketID := setupFixture()

	sig := val.ComputeSignature(ticketID, eventID)
	req := database.ScanRequest{
		DeviceIdentifier: "DEV-01",
		DeviceSecret:     "secret123",
		TicketID:         ticketID,
		Signature:        sig,
	}

	resp, err := val.Validate(context.Background(), req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !resp.Accepted || resp.Result != database.ScanResultValid {
		t.Fatalf("expected VALID and accepted=true, got result=%s, accepted=%v", resp.Result, resp.Accepted)
	}

	// Verify redemption recorded in redemptions and sync_inbox queued
	if len(repo.redemptions) != 1 {
		t.Fatalf("expected 1 redemption in redemptions table, got %d", len(repo.redemptions))
	}
	if len(repo.inbox) != 1 || repo.inbox[0].SourceType != "REDEMPTION" {
		t.Fatalf("expected 1 REDEMPTION sync_inbox record, got %d", len(repo.inbox))
	}
}

// TestRequirement2_TicketScannedAtDifferentEventGate
func TestRequirement2_TicketScannedAtDifferentEventGate(t *testing.T) {
	val, repo, authMock, _, gateAID, gateBID, ticketID := setupFixture()
	otherEventID := "88888888-8888-8888-8888-888888888888"

	// Assign device to a gate for a completely different event
	authMock.devices["DEV-01"].EventID = otherEventID
	authMock.assignments[gateAID] = &auth.AuthorizedDeviceContext{
		DeviceID:         gateAID,
		DeviceIdentifier: "DEV-01",
		EventID:          otherEventID,
		EventGateID:      gateBID,
		GateCode:         "DEV-01",
		GateName:         "Different Event Gate",
	}

	// Register other event in repo so event lookup succeeds
	now := time.Now().UTC()
	repo.events[otherEventID] = &database.Event{
		ID:          otherEventID,
		EventName:   "Other Event",
		EventStart:  now.Add(-1 * time.Hour),
		EventEnd:    now.Add(5 * time.Hour),
		EventStatus: database.EventStatusPublished,
	}
	repo.metadata[otherEventID] = &database.ManifestMetadata{
		EventID:         otherEventID,
		ManifestVersion: 1,
		ManifestHash:    "hash-meta-other",
		Signature:       "sig-meta-other",
		SignerKeyID:     "signer-001",
		IssuedAt:        now.Add(-2 * time.Hour),
		ExpiresAt:       now.Add(6 * time.Hour),
	}

	sig := val.ComputeSignature(ticketID, otherEventID)
	req := database.ScanRequest{
		DeviceIdentifier: "DEV-01",
		DeviceSecret:     "secret123",
		TicketID:         ticketID,
		Signature:        sig,
	}

	resp, err := val.Validate(context.Background(), req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Accepted || resp.Result != database.ScanResultWrongGate {
		t.Fatalf("expected WRONG_GATE rejection, got %s", resp.Result)
	}

	// Ticket must not be consumed
	if repo.tickets[ticketID].TicketStatus != database.TicketStatusActive {
		t.Fatalf("ticket must remain ACTIVE after wrong-gate rejection")
	}

	// Rejection must be queued in sync_inbox
	if len(repo.inbox) != 1 || repo.inbox[0].SourceType != "SCAN_REJECTION" {
		t.Fatalf("expected 1 SCAN_REJECTION in sync_inbox, got %d", len(repo.inbox))
	}
}

// TestRequirement3_TicketAlreadyAccepted_Duplicate
func TestRequirement3_TicketAlreadyAccepted_Duplicate(t *testing.T) {
	val, _, _, eventID, _, _, ticketID := setupFixture()

	sig := val.ComputeSignature(ticketID, eventID)
	req := database.ScanRequest{
		DeviceIdentifier: "DEV-01",
		DeviceSecret:     "secret123",
		TicketID:         ticketID,
		Signature:        sig,
	}

	// First scan succeeds
	resp1, err := val.Validate(context.Background(), req)
	if err != nil || !resp1.Accepted {
		t.Fatalf("first scan should succeed, got %v", err)
	}

	// Second scan must be DUPLICATE
	resp2, err := val.Validate(context.Background(), req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp2.Accepted || resp2.Result != database.ScanResultDuplicate {
		t.Fatalf("expected DUPLICATE on second scan, got %s", resp2.Result)
	}
}

// TestRequirement4_CancelledTicket
func TestRequirement4_CancelledTicket(t *testing.T) {
	val, repo, _, eventID, _, _, ticketID := setupFixture()
	repo.tickets[ticketID].TicketStatus = database.TicketStatusCancelled

	sig := val.ComputeSignature(ticketID, eventID)
	req := database.ScanRequest{
		DeviceIdentifier: "DEV-01",
		DeviceSecret:     "secret123",
		TicketID:         ticketID,
		Signature:        sig,
	}

	resp, err := val.Validate(context.Background(), req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Accepted || resp.Result != database.ScanResultCancelled {
		t.Fatalf("expected CANCELLED, got %s", resp.Result)
	}
}

// TestRequirement5_RevokedTicket
func TestRequirement5_RevokedTicket(t *testing.T) {
	val, repo, _, eventID, _, _, ticketID := setupFixture()
	repo.revoked[ticketID+":"+eventID] = true

	sig := val.ComputeSignature(ticketID, eventID)
	req := database.ScanRequest{
		DeviceIdentifier: "DEV-01",
		DeviceSecret:     "secret123",
		TicketID:         ticketID,
		Signature:        sig,
	}

	resp, err := val.Validate(context.Background(), req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Accepted || resp.Result != database.ScanResultRevoked {
		t.Fatalf("expected REVOKED, got %s", resp.Result)
	}
}

// TestRequirement6_ExpiredTicket
func TestRequirement6_ExpiredTicket(t *testing.T) {
	val, repo, _, eventID, _, _, ticketID := setupFixture()
	repo.tickets[ticketID].ValidUntil = time.Now().UTC().Add(-1 * time.Hour)

	sig := val.ComputeSignature(ticketID, eventID)
	req := database.ScanRequest{
		DeviceIdentifier: "DEV-01",
		DeviceSecret:     "secret123",
		TicketID:         ticketID,
		Signature:        sig,
	}

	resp, err := val.Validate(context.Background(), req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Accepted || resp.Result != database.ScanResultExpired {
		t.Fatalf("expected EXPIRED, got %s", resp.Result)
	}
}

// TestRequirement7_InvalidTicketSignature
func TestRequirement7_InvalidTicketSignature(t *testing.T) {
	val, _, _, _, _, _, ticketID := setupFixture()

	req := database.ScanRequest{
		DeviceIdentifier: "DEV-01",
		DeviceSecret:     "secret123",
		TicketID:         ticketID,
		Signature:        "invalid-tampered-signature-hex",
	}

	resp, err := val.Validate(context.Background(), req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Accepted || resp.Result != database.ScanResultInvalid {
		t.Fatalf("expected INVALID, got %s", resp.Result)
	}
}

// TestRequirement8_UnauthorizedGateDevice
func TestRequirement8_UnauthorizedGateDevice(t *testing.T) {
	val, _, authMock, eventID, _, _, ticketID := setupFixture()
	authMock.unauthorized = true

	sig := val.ComputeSignature(ticketID, eventID)
	req := database.ScanRequest{
		DeviceIdentifier: "UNKNOWN-DEVICE",
		DeviceSecret:     "wrong-secret",
		TicketID:         ticketID,
		Signature:        sig,
	}

	resp, err := val.Validate(context.Background(), req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Accepted || resp.Result != database.ScanResultDeviceUnauthorized {
		t.Fatalf("expected DEVICE_UNAUTHORIZED, got %s", resp.Result)
	}
}

// TestRequirement9_ConcurrentGatesScanningSameTicket
func TestRequirement9_ConcurrentGatesScanningSameTicket(t *testing.T) {
	val, repo, _, eventID, _, _, ticketID := setupFixture()
	sig := val.ComputeSignature(ticketID, eventID)

	var acceptedCount int32
	var duplicateCount int32
	var wg sync.WaitGroup

	concurrentScanners := 10
	wg.Add(concurrentScanners)

	for i := 0; i < concurrentScanners; i++ {
		go func() {
			defer wg.Done()
			req := database.ScanRequest{
				DeviceIdentifier: "DEV-01",
				DeviceSecret:     "secret123",
				TicketID:         ticketID,
				Signature:        sig,
			}

			resp, err := val.Validate(context.Background(), req)
			if err != nil {
				t.Errorf("unexpected error in concurrent scan: %v", err)
				return
			}
			if resp.Accepted && resp.Result == database.ScanResultValid {
				atomic.AddInt32(&acceptedCount, 1)
			} else if resp.Result == database.ScanResultDuplicate {
				atomic.AddInt32(&duplicateCount, 1)
			}
		}()
	}

	wg.Wait()

	if acceptedCount != 1 {
		t.Fatalf("atomic duplicate prevention failed: accepted %d times (MUST be exactly 1)", acceptedCount)
	}
	if duplicateCount != int32(concurrentScanners-1) {
		t.Fatalf("expected %d duplicates, got %d", concurrentScanners-1, duplicateCount)
	}

	// Verify only 1 accepted scan in redemptions table
	if len(repo.redemptions) != 1 {
		t.Fatalf("expected exactly 1 redemption record in repo, got %d", len(repo.redemptions))
	}
}

// TestRequirement13_GateDeviceReassignment
func TestRequirement13_GateDeviceReassignment(t *testing.T) {
	_, _, authMock, eventID, gateAID, gateBID, _ := setupFixture()

	// Initial assignment: Gate A
	assign1, err := authMock.Authenticate(context.Background(), "DEV-01", "secret123")
	if err != nil || assign1.EventGateID != gateAID {
		t.Fatalf("expected Gate A assignment, got %v", assign1)
	}

	// Reassign device to Gate B
	authMock.devices["DEV-01"].ID = gateBID
	authMock.devices["DEV-01"].GateName = "Gate B West"
	authMock.assignments[gateBID] = &auth.AuthorizedDeviceContext{
		DeviceID:         gateBID,
		DeviceIdentifier: "DEV-01",
		EventID:          eventID,
		EventGateID:      gateBID,
		GateCode:         "DEV-01",
		GateName:         "Gate B West",
	}

	assign2, err := authMock.Authenticate(context.Background(), "DEV-01", "secret123")
	if err != nil || assign2.EventGateID != gateBID {
		t.Fatalf("expected reassignment to Gate B, got %v", assign2)
	}
}

// TestRequirement14_EventSpecificGateValidation
func TestRequirement14_EventSpecificGateValidation(t *testing.T) {
	val, repo, _, _, _, _, ticketID := setupFixture()

	// Assign ticket to an entirely different event
	foreignEventID := "99999999-9999-9999-9999-999999999999"
	repo.tickets[ticketID].EventID = foreignEventID

	sig := val.ComputeSignature(ticketID, foreignEventID)
	req := database.ScanRequest{
		DeviceIdentifier: "DEV-01",
		DeviceSecret:     "secret123",
		TicketID:         ticketID,
		Signature:        sig,
	}

	resp, err := val.Validate(context.Background(), req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Accepted || resp.Result != database.ScanResultInvalid {
		t.Fatalf("expected INVALID for foreign event ticket, got %s", resp.Result)
	}
}

func TestHashGlobalQRToken(t *testing.T) {
	token := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	got, err := HashGlobalQRToken(token)
	if err != nil {
		t.Fatalf("HashGlobalQRToken returned error: %v", err)
	}
	sum := sha256.Sum256([]byte(token))
	want := hex.EncodeToString(sum[:])
	if got != want {
		t.Fatalf("hash mismatch: got %s want %s", got, want)
	}
}

func TestHashGlobalQRTokenRejectsMalformedValues(t *testing.T) {
	cases := []string{"", "short", strings.Repeat("z", 64), strings.Repeat("a", 63)}
	for _, token := range cases {
		if _, err := HashGlobalQRToken(token); err == nil {
			t.Errorf("expected malformed token %q to fail", token)
		}
	}
}

func TestRequirement4_OfflineRedemptionCreatesOfflineSyncSource(t *testing.T) {
	val, repo, _, _, _, _, ticketID := setupFixture()

	req := database.ScanRequest{
		DeviceIdentifier: "DEV-01",
		DeviceSecret:     "secret123",
		TicketID:         ticketID,
	}

	resp, err := val.Validate(context.Background(), req)
	if err != nil {
		t.Fatalf("validation failed: %v", err)
	}
	if !resp.Accepted {
		t.Fatalf("expected ticket to be accepted, got: %s (%s)", resp.Result, resp.Message)
	}

	redemption, ok := repo.redemptions[ticketID]
	if !ok {
		t.Fatalf("expected redemption record in repository")
	}
	if redemption.Source != "OFFLINE_SYNC" {
		t.Fatalf("expected redemption source to be 'OFFLINE_SYNC', got '%s'", redemption.Source)
	}

	// Verify inbox record payload contains OFFLINE_SYNC source
	if len(repo.inbox) == 0 {
		t.Fatalf("expected sync_inbox record to be queued")
	}
	var payload map[string]any
	if err := json.Unmarshal(repo.inbox[0].Payload, &payload); err != nil {
		t.Fatalf("failed to unmarshal inbox payload: %v", err)
	}
	if payload["source"] != "OFFLINE_SYNC" {
		t.Fatalf("expected sync payload source 'OFFLINE_SYNC', got '%v'", payload["source"])
	}
}

func TestRequirement7_MissingManifestRejectsOfflineScan(t *testing.T) {
	val, repo, _, eventID, _, _, ticketID := setupFixture()

	// Simulate missing manifest metadata
	delete(repo.metadata, eventID)

	req := database.ScanRequest{
		DeviceIdentifier: "DEV-01",
		DeviceSecret:     "secret123",
		TicketID:         ticketID,
	}

	resp, err := val.Validate(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Accepted {
		t.Fatalf("expected scan to be rejected when manifest is missing")
	}
	if resp.Result != database.ScanResultInvalid {
		t.Fatalf("expected ScanResultInvalid when manifest is missing, got: %s", resp.Result)
	}
}

func TestRequirement7_ExpiredManifestRejectsOfflineScan(t *testing.T) {
	val, repo, _, eventID, _, _, ticketID := setupFixture()

	now := time.Now().UTC()
	// Simulate expired manifest metadata
	repo.metadata[eventID].ExpiresAt = now.Add(-10 * time.Minute)

	req := database.ScanRequest{
		DeviceIdentifier: "DEV-01",
		DeviceSecret:     "secret123",
		TicketID:         ticketID,
	}

	resp, err := val.Validate(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Accepted {
		t.Fatalf("expected scan to be rejected when manifest has expired")
	}
	if resp.Result != database.ScanResultExpired {
		t.Fatalf("expected ScanResultExpired, got: %s", resp.Result)
	}
}

func TestRequirement7_NotYetValidManifestRejectsOfflineScan(t *testing.T) {
	val, repo, _, eventID, _, _, ticketID := setupFixture()

	now := time.Now().UTC()
	// Simulate manifest issued in the future
	repo.metadata[eventID].IssuedAt = now.Add(1 * time.Hour)

	req := database.ScanRequest{
		DeviceIdentifier: "DEV-01",
		DeviceSecret:     "secret123",
		TicketID:         ticketID,
	}

	resp, err := val.Validate(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Accepted {
		t.Fatalf("expected scan to be rejected when manifest is not yet valid")
	}
	if resp.Result != database.ScanResultInvalid {
		t.Fatalf("expected ScanResultInvalid, got: %s", resp.Result)
	}
}

func TestRequirement_RevokedDeviceCredentialsRejectsScan(t *testing.T) {
	val, _, authMock, _, _, _, ticketID := setupFixture()

	now := time.Now().UTC()
	revokedAt := now.Add(-1 * time.Hour)
	authMock.credentials["DEV-01"] = &database.DeviceCredential{
		DeviceIdentifier: "DEV-01",
		RevokedAt:        &revokedAt,
	}

	req := database.ScanRequest{
		DeviceIdentifier: "DEV-01",
		DeviceSecret:     "secret123",
		TicketID:         ticketID,
	}

	resp, err := val.Validate(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Accepted {
		t.Fatalf("expected scan to be rejected for revoked device credentials")
	}
	if resp.Result != database.ScanResultDeviceUnauthorized {
		t.Fatalf("expected ScanResultDeviceUnauthorized, got %s", resp.Result)
	}
}

func TestRequirement_ExpiredDeviceCredentialsRejectsScan(t *testing.T) {
	val, _, authMock, _, _, _, ticketID := setupFixture()

	now := time.Now().UTC()
	authMock.credentials["DEV-01"] = &database.DeviceCredential{
		DeviceIdentifier: "DEV-01",
		ExpiresAt:        now.Add(-10 * time.Minute),
	}

	req := database.ScanRequest{
		DeviceIdentifier: "DEV-01",
		DeviceSecret:     "secret123",
		TicketID:         ticketID,
	}

	resp, err := val.Validate(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Accepted {
		t.Fatalf("expected scan to be rejected for expired device credentials")
	}
	if resp.Result != database.ScanResultDeviceUnauthorized {
		t.Fatalf("expected ScanResultDeviceUnauthorized, got %s", resp.Result)
	}
}
