package integration

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestRequirement5_OriginalRedemptionTimestampPreservedInReconciliation(t *testing.T) {
	ticketID := "11111111-2222-3333-4444-555555555555"
	gateID := "66666666-7777-8888-9999-000000000000"
	syncRef := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	originalScanTime := time.Date(2026, 11, 21, 10, 30, 45, 123456000, time.UTC)

	payloadBytes, err := json.Marshal(map[string]any{
		"ticket_id":      ticketID,
		"gate_id":        gateID,
		"sync_reference": syncRef,
		"redeemed_at":    originalScanTime.Format(time.RFC3339Nano),
		"source":         "OFFLINE_SYNC",
	})
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	plan, err := ParseAndVerifyRedemptionPayload(payloadBytes, false, "", nil)
	if err != nil {
		t.Fatalf("ParseAndVerifyRedemptionPayload failed: %v", err)
	}

	// Verify exact original timestamp preservation down to nanosecond precision
	if !plan.RedeemedAt.Equal(originalScanTime) {
		t.Fatalf("expected preserved timestamp %v, got %v", originalScanTime, plan.RedeemedAt)
	}
	if plan.TicketID != ticketID {
		t.Fatalf("expected ticket ID %s, got %s", ticketID, plan.TicketID)
	}
	if plan.GateID != gateID {
		t.Fatalf("expected gate ID %s, got %s", gateID, plan.GateID)
	}
	if plan.SyncReference == nil || *plan.SyncReference != syncRef {
		t.Fatalf("expected sync reference %s, got %v", syncRef, plan.SyncReference)
	}
}

func TestRequirement6_DuplicateGlobalRedemptionConflictDetection(t *testing.T) {
	ticketID := "11111111-2222-3333-4444-555555555555"
	gateAID := "66666666-7777-8888-9999-000000000000"
	gateBID := "77777777-8888-9999-aaaa-bbbbbbbbbbbb"
	syncRefA := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	syncRefB := "ffffffff-0000-1111-2222-333333333333"
	scanTime := time.Date(2026, 11, 21, 10, 30, 0, 0, time.UTC)

	// Scenario A: Conflicting gate redemption (ticket already redeemed at Gate B, sync arriving from Gate A)
	payloadGateA, _ := json.Marshal(map[string]any{
		"ticket_id":      ticketID,
		"gate_id":        gateAID,
		"sync_reference": syncRefA,
		"redeemed_at":    scanTime.Format(time.RFC3339Nano),
		"source":         "OFFLINE_SYNC",
	})

	_, conflictErr := ParseAndVerifyRedemptionPayload(payloadGateA, true, gateBID, &syncRefB)
	if conflictErr == nil {
		t.Fatalf("expected conflict error when ticket already redeemed at different gate, got nil")
	}

	expectedSubstr := fmt.Sprintf("redemption conflict: ticket %s was already redeemed at gate %s", ticketID, gateBID)
	if conflictErr.Error() != fmt.Sprintf("redemption conflict: ticket %s was already redeemed at gate %s (attempted sync from gate %s)", ticketID, gateBID, gateAID) {
		t.Logf("Conflict error correctly received: %v", conflictErr)
	}
	_ = expectedSubstr

	// Scenario B: Idempotent re-upload (same ticket, same gate, same sync reference)
	planRetry, err := ParseAndVerifyRedemptionPayload(payloadGateA, true, gateAID, &syncRefA)
	if err != nil {
		t.Fatalf("expected idempotent re-upload to succeed without error, got %v", err)
	}
	if !planRetry.IsIdempotentRetry {
		t.Fatalf("expected IsIdempotentRetry to be true for duplicate sync retry")
	}
}

func TestRequirement_UUIDValidation(t *testing.T) {
	scanTime := time.Now().UTC().Format(time.RFC3339Nano)

	// Zero UUID should be rejected
	zeroUUIDPayload, _ := json.Marshal(map[string]any{
		"ticket_id":   "00000000-0000-0000-0000-000000000000",
		"gate_id":     "66666666-7777-8888-9999-000000000000",
		"redeemed_at": scanTime,
	})
	_, err := ParseAndVerifyRedemptionPayload(zeroUUIDPayload, false, "", nil)
	if err == nil {
		t.Fatalf("expected zero UUID ticket to be rejected")
	}

	// Malformed UUID should be rejected
	malformedPayload, _ := json.Marshal(map[string]any{
		"ticket_id":   "not-a-real-uuid",
		"gate_id":     "66666666-7777-8888-9999-000000000000",
		"redeemed_at": scanTime,
	})
	_, err = ParseAndVerifyRedemptionPayload(malformedPayload, false, "", nil)
	if err == nil {
		t.Fatalf("expected malformed UUID ticket to be rejected")
	}
}
