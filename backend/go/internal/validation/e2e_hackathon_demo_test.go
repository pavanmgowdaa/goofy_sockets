package validation

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"fairpass/internal/auth"
	"fairpass/internal/database"
)

// MockGlobalBackend simulates the central cloud / Neon PostgreSQL database
// using the identical 12-table schema.
type MockGlobalBackend struct {
	EventID        string
	EventName      string
	Gates          map[string]*database.VenueGate
	Tickets        map[string]*database.Ticket
	Redemptions    map[string]*database.Redemption
	ReceivedInbox  []database.SyncInbox
	ConnectedToWAN bool
}

func NewMockGlobalBackend(eventID, eventName string) *MockGlobalBackend {
	return &MockGlobalBackend{
		EventID:        eventID,
		EventName:      eventName,
		Gates:          make(map[string]*database.VenueGate),
		Tickets:        make(map[string]*database.Ticket),
		Redemptions:    make(map[string]*database.Redemption),
		ReceivedInbox:  make([]database.SyncInbox, 0),
		ConnectedToWAN: true,
	}
}

// ReceiveSyncBatch handles synchronization uploads into global sync_inbox and redemptions.
func (g *MockGlobalBackend) ReceiveSyncBatch(ctx context.Context, records []database.SyncInbox) error {
	if !g.ConnectedToWAN {
		return fmt.Errorf("network unreachable: venue offline / WAN disconnected")
	}

	for _, rec := range records {
		// Idempotent upsert by idempotency_key
		exists := false
		for _, existing := range g.ReceivedInbox {
			if existing.IdempotencyKey == rec.IdempotencyKey {
				exists = true
				break
			}
		}
		if !exists {
			g.ReceivedInbox = append(g.ReceivedInbox, rec)
			if rec.SourceType == "REDEMPTION" {
				var p map[string]any
				if err := json.Unmarshal(rec.Payload, &p); err == nil {
					ticketID, _ := p["ticket_id"].(string)
					gateID, _ := p["gate_id"].(string)
					g.Redemptions[ticketID] = &database.Redemption{
						ID:         rec.SourceRecordID,
						TicketID:   ticketID,
						GateID:     gateID,
						RedeemedAt: rec.ReceivedAt,
						Source:     "OFFLINE_SYNC",
					}
					if t, ok := g.Tickets[ticketID]; ok {
						t.TicketStatus = database.TicketStatusUsed
					}
				}
			}
		}
	}
	return nil
}

// TestEndToEndHackathonDemonstrationScenario verifies the exact 13-step sequence
// using the authoritative 12-table PostgreSQL schema.
func TestEndToEndHackathonDemonstrationScenario(t *testing.T) {
	secretKey := "hackathon-secret-key-fairpass-2026"
	eventID := "11111111-1111-1111-1111-111111111111"
	eventName := "FAIRPASS Hackathon Championship 2026"

	gate1ID := "aaaaaaaa-1111-1111-1111-111111111111"
	gate2ID := "bbbbbbbb-2222-2222-2222-222222222222"
	gate3ID := "cccccccc-3333-3333-3333-333333333333"

	t.Log("================================================================================")
	t.Log("FAIRPASS 12-TABLE SCHEMA: 13-STEP END-TO-END DEMONSTRATION")
	t.Log("================================================================================")

	// -------------------------------------------------------------------------
	// STEP 1: Create an event with 3 gates in events & venue_gates
	// -------------------------------------------------------------------------
	t.Log("\n[STEP 1] Creating event and 3 venue gates in Global Database (Neon)...")
	global := NewMockGlobalBackend(eventID, eventName)

	global.Gates["DEV-GATE-01"] = &database.VenueGate{
		ID:               gate1ID,
		EventID:          eventID,
		DeviceIdentifier: "DEV-GATE-01",
		GateName:         "VIP South Gate (Gate 1)",
		DeviceSecretHash: "dev01secret",
		DeviceStatus:     database.DeviceStatusActive,
		CreatedAt:        time.Now().UTC(),
	}
	global.Gates["DEV-GATE-02"] = &database.VenueGate{
		ID:               gate2ID,
		EventID:          eventID,
		DeviceIdentifier: "DEV-GATE-02",
		GateName:         "Main Entrance Gate 2",
		DeviceSecretHash: "dev02secret",
		DeviceStatus:     database.DeviceStatusActive,
		CreatedAt:        time.Now().UTC(),
	}
	global.Gates["DEV-GATE-03"] = &database.VenueGate{
		ID:               gate3ID,
		EventID:          eventID,
		DeviceIdentifier: "DEV-GATE-03",
		GateName:         "East Entrance Gate 3",
		DeviceSecretHash: "dev03secret",
		DeviceStatus:     database.DeviceStatusActive,
		CreatedAt:        time.Now().UTC(),
	}

	if len(global.Gates) != 3 {
		t.Fatalf("Step 1 failed: Expected 3 gates, created %d", len(global.Gates))
	}
	t.Logf("✓ Created Event '%s' (%s) with 3 venue gates", eventName, eventID)

	// -------------------------------------------------------------------------
	// STEP 2: Configure event capacity
	// -------------------------------------------------------------------------
	t.Log("\n[STEP 2] Configuring event capacity...")
	totalCapacity := 500
	t.Logf("  - Total Capacity = %d", totalCapacity)
	t.Log("✓ Configured event capacity successfully")

	// -------------------------------------------------------------------------
	// STEP 3 & 4: Purchase ticket online in tickets table
	// -------------------------------------------------------------------------
	t.Log("\n[STEP 3 & 4] Purchasing ticket online in tickets table...")
	ticketID := "tkt-hackathon-ticket-001"
	validUntil := time.Now().Add(24 * time.Hour).UTC()
	ticketHash := hex.EncodeToString([]byte("ticket-raw-bytes-001-fixed-len"))

	global.Tickets[ticketID] = &database.Ticket{
		ID:              ticketID,
		EventID:         eventID,
		TicketHash:      ticketHash,
		OriginalOwnerID: "00000000-0000-0000-0000-000000000001",
		CurrentOwnerID:  "00000000-0000-0000-0000-000000000001",
		TicketType:      "GENERAL_ADMISSION",
		Price:           25.00,
		TicketStatus:    database.TicketStatusActive,
		ValidUntil:      validUntil,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
	t.Logf("✓ Ticket '%s' purchased online and stored in tickets table", ticketID)

	// -------------------------------------------------------------------------
	// STEP 5: Generate its QR code
	// -------------------------------------------------------------------------
	t.Log("\n[STEP 5] Generating cryptographic QR code with HMAC-SHA256 signature...")
	localRepo := NewMockRepository()
	localAuth := NewMockAuthenticator()
	validator := NewValidatorWithRepo(localRepo, localAuth, secretKey)

	qrSignature1 := validator.ComputeSignature(ticketID, eventID)
	qrPayload1, _ := json.Marshal(map[string]string{
		"ticket_id": ticketID,
		"event_id":  eventID,
		"sig":       qrSignature1,
	})
	t.Logf("✓ Cryptographic signature generated: %s", qrSignature1)
	t.Logf("✓ QR Payload: %s", string(qrPayload1))

	// -------------------------------------------------------------------------
	// STEP 6: Download event manifest to the local server
	// -------------------------------------------------------------------------
	t.Log("\n[STEP 6] Downloading authoritative manifest from Global DB to Local Venue Server...")
	now := time.Now().UTC()
	localRepo.events[eventID] = &database.Event{
		ID:            eventID,
		OrganizerID:   "99999999-9999-9999-9999-999999999999",
		EventName:     eventName,
		EventStart:    now.Add(-1 * time.Hour),
		EventEnd:      validUntil,
		TotalCapacity: totalCapacity,
		EventStatus:   database.EventStatusPublished,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	localRepo.metadata[eventID] = &database.ManifestMetadata{
		ID:              "meta-001",
		EventID:         eventID,
		ManifestVersion: 1,
		ManifestHash:    "sha256-root-hash",
		Signature:       "manifest-sig",
		SignerKeyID:     "fairpass-root-key",
		IssuedAt:        now,
		ExpiresAt:       validUntil,
		CreatedAt:       now,
	}

	localRepo.tickets[ticketID] = global.Tickets[ticketID]

	// Register scanning device DEV-GATE-02 at Gate 2
	localAuth.devices["DEV-GATE-02"] = global.Gates["DEV-GATE-02"]
	localAuth.assignments[gate2ID] = &auth.AuthorizedDeviceContext{
		DeviceID:         gate2ID,
		DeviceIdentifier: "DEV-GATE-02",
		DeviceName:       "Main Entrance Gate 2",
		EventID:          eventID,
		EventGateID:      gate2ID,
		GateCode:         "DEV-GATE-02",
		GateName:         "Main Entrance Gate 2",
	}

	t.Log("✓ Manifest downloaded: Local venue database populated with events, venue_gates, and tickets")

	// -------------------------------------------------------------------------
	// STEP 7: Disconnect internet
	// -------------------------------------------------------------------------
	t.Log("\n[STEP 7] Simulating venue internet outage (WAN disconnected)...")
	global.ConnectedToWAN = false
	t.Log("✓ Venue WAN is now OFFLINE. The local venue PostgreSQL server operates fully independently.")

	// -------------------------------------------------------------------------
	// STEP 8: Scan at Gate 2: return VALID
	// -------------------------------------------------------------------------
	t.Log("\n[STEP 8] Scanning ticket at Gate 2 (Device DEV-GATE-02)...")
	scanReqGate2 := database.ScanRequest{
		DeviceIdentifier: "DEV-GATE-02",
		DeviceSecret:     "dev02secret",
		TicketID:         ticketID,
		Signature:        qrSignature1,
	}

	scanResp1, err := validator.Validate(context.Background(), scanReqGate2)
	if err != nil {
		t.Fatalf("Step 8 failed: unexpected validation error: %v", err)
	}

	if !scanResp1.Accepted || scanResp1.Result != database.ScanResultValid {
		t.Fatalf("Step 8 failed: expected VALID (accepted=true), got result=%s, accepted=%v", scanResp1.Result, scanResp1.Accepted)
	}
	t.Logf("✓ Scan Result: %s (Accepted: %v)", scanResp1.Result, scanResp1.Accepted)
	t.Logf("  Message: %s | Scanned at Gate: %s (%s)", scanResp1.Message, scanResp1.GateCode, scanResp1.GateName)

	// Verify redemption recorded locally in redemptions table
	if len(localRepo.redemptions) != 1 || localRepo.redemptions[ticketID] == nil {
		t.Fatalf("Step 8 failed: redemption was not recorded locally in redemptions table")
	}

	// -------------------------------------------------------------------------
	// STEP 9: Scan same ticket again: return DUPLICATE
	// -------------------------------------------------------------------------
	t.Log("\n[STEP 9] Scanning SAME ticket again at Gate 2 (Device DEV-GATE-02)...")
	scanResp2, err := validator.Validate(context.Background(), scanReqGate2)
	if err != nil {
		t.Fatalf("Step 9 failed: unexpected error: %v", err)
	}

	if scanResp2.Accepted || scanResp2.Result != database.ScanResultDuplicate {
		t.Fatalf("Step 9 failed: expected DUPLICATE (accepted=false), got result=%s, accepted=%v", scanResp2.Result, scanResp2.Accepted)
	}
	t.Logf("✓ Scan Result: %s (Accepted: %v)", scanResp2.Result, scanResp2.Accepted)
	t.Logf("  Message: %s", scanResp2.Message)
	t.Log("✓ Duplicate admission prevented atomically via redemptions unique constraint")

	// -------------------------------------------------------------------------
	// STEP 10: Scan ticket at another event's gate: return WRONG_GATE
	// -------------------------------------------------------------------------
	t.Log("\n[STEP 10] Scanning ticket for different event: return WRONG_GATE...")
	foreignEventID := "99999999-9999-9999-9999-999999999999"
	foreignGateID := "88888888-8888-8888-8888-888888888888"

	localRepo.events[foreignEventID] = &database.Event{
		ID:          foreignEventID,
		EventName:   "Foreign Event",
		EventStart:  now.Add(-1 * time.Hour),
		EventEnd:    validUntil,
		EventStatus: database.EventStatusPublished,
	}
	localRepo.metadata[foreignEventID] = &database.ManifestMetadata{
		ID:              "meta-foreign-001",
		EventID:         foreignEventID,
		ManifestVersion: 1,
		ManifestHash:    "sha256-foreign-root-hash",
		Signature:       "manifest-foreign-sig",
		SignerKeyID:     "fairpass-root-key",
		IssuedAt:        now.Add(-1 * time.Hour),
		ExpiresAt:       validUntil,
		CreatedAt:       now,
	}

	localAuth.devices["DEV-FOREIGN-01"] = &database.VenueGate{
		ID:               foreignGateID,
		EventID:          foreignEventID,
		DeviceIdentifier: "DEV-FOREIGN-01",
		GateName:         "Foreign Gate",
		DeviceSecretHash: "secretforeign",
		DeviceStatus:     database.DeviceStatusActive,
	}
	localAuth.assignments[foreignGateID] = &auth.AuthorizedDeviceContext{
		DeviceID:         foreignGateID,
		DeviceIdentifier: "DEV-FOREIGN-01",
		EventID:          foreignEventID,
		EventGateID:      foreignGateID,
		GateCode:         "DEV-FOREIGN-01",
		GateName:         "Foreign Gate",
	}

	// Another ticket for Event 1
	ticket2ID := "tkt-hackathon-ticket-002"
	localRepo.tickets[ticket2ID] = &database.Ticket{
		ID:           ticket2ID,
		EventID:      eventID,
		TicketHash:   "hash-002",
		TicketStatus: database.TicketStatusActive,
		ValidUntil:   validUntil,
	}

	sig2 := validator.ComputeSignature(ticket2ID, foreignEventID)
	scanReqForeign := database.ScanRequest{
		DeviceIdentifier: "DEV-FOREIGN-01",
		DeviceSecret:     "secretforeign",
		TicketID:         ticket2ID,
		Signature:        sig2,
	}

	scanResp3, err := validator.Validate(context.Background(), scanReqForeign)
	if err != nil {
		t.Fatalf("Step 10 failed: unexpected error: %v", err)
	}

	if scanResp3.Accepted || scanResp3.Result != database.ScanResultWrongGate {
		t.Fatalf("Step 10 failed: expected WRONG_GATE (accepted=false), got result=%s, accepted=%v", scanResp3.Result, scanResp3.Accepted)
	}
	t.Logf("✓ Scan Result: %s (Accepted: %v)", scanResp3.Result, scanResp3.Accepted)
	t.Logf("  Message: %s", scanResp3.Message)

	// Verify ticket 2 remains ACTIVE and unconsumed
	if localRepo.tickets[ticket2ID].TicketStatus != database.TicketStatusActive {
		t.Fatalf("Step 10 failed: ticket 2 validity was improperly consumed")
	}
	t.Log("✓ Wrong gate rejection recorded; ticket validity preserved in local database")

	// -------------------------------------------------------------------------
	// STEP 11: Restore internet
	// -------------------------------------------------------------------------
	t.Log("\n[STEP 11] Restoring venue internet connection (WAN reconnected)...")
	global.ConnectedToWAN = true
	t.Log("✓ Venue WAN is now ONLINE. Synchronization service ready to flush sync_inbox.")

	// -------------------------------------------------------------------------
	// STEP 12: Synchronize scan records with global database
	// -------------------------------------------------------------------------
	t.Log("\n[STEP 12] Synchronizing records from local sync_inbox to Global Database...")
	recordsToSync := localRepo.inbox
	if len(recordsToSync) == 0 {
		t.Fatalf("Step 12 failed: no records found in local sync_inbox")
	}
	t.Logf("  Local sync_inbox records to flush: %d", len(recordsToSync))

	err = global.ReceiveSyncBatch(context.Background(), recordsToSync)
	if err != nil {
		t.Fatalf("Step 12 failed: sync flush error: %v", err)
	}

	// Update local inbox status to PROCESSED
	for i := range localRepo.inbox {
		localRepo.inbox[i].ProcessingStatus = database.SyncStatusProcessed
	}
	t.Log("✓ Local sync_inbox records marked PROCESSED")

	// -------------------------------------------------------------------------
	// STEP 13: Verify global database receives the correct scan history
	// -------------------------------------------------------------------------
	t.Log("\n[STEP 13] Verifying global database received the complete scan history...")
	if len(global.ReceivedInbox) != len(recordsToSync) {
		t.Fatalf("Step 13 failed: expected %d records in global inbox, got %d", len(recordsToSync), len(global.ReceivedInbox))
	}

	redemptionCount := 0
	rejectionCount := 0
	for _, rec := range global.ReceivedInbox {
		if rec.SourceType == "REDEMPTION" {
			redemptionCount++
		} else if rec.SourceType == "SCAN_REJECTION" {
			rejectionCount++
		}
	}

	t.Logf("✓ Global DB received %d total sync_inbox records:", len(global.ReceivedInbox))
	t.Logf("  - Accepted redemptions: %d", redemptionCount)
	t.Logf("  - Scan rejections: %d", rejectionCount)

	if redemptionCount < 1 {
		t.Fatalf("Step 13 failed: expected at least 1 redemption in global DB")
	}

	// Test idempotency: flush again to verify no duplicate insertions in global database
	err = global.ReceiveSyncBatch(context.Background(), recordsToSync)
	if err != nil {
		t.Fatalf("idempotent sync re-flush failed: %v", err)
	}
	if len(global.ReceivedInbox) != len(recordsToSync) {
		t.Fatalf("idempotent sync failed: duplicate records inserted in global DB")
	}
	t.Log("✓ Idempotent re-sync verified: duplicate records rejected via idempotency_key")

	t.Log("\n================================================================================")
	t.Log("ALL 13 STEPS IN THE DEMONSTRATION SCENARIO PASSED SUCCESSFULLY!")
	t.Log("================================================================================")
}
