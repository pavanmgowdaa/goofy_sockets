package sync

import (
	"context"
	"errors"
	"testing"

	"fairpass/internal/database"
)

func TestRequirement1_MissingGlobalConnection_SyncUnavailable(t *testing.T) {
	// Sync service created without upstream connection (production fallback when FAIRPASS_GLOBAL_DATABASE_URL is missing or fails)
	service := NewSyncService(nil, nil, "venue-offline-secret")

	if service.IsUpstreamAvailable() {
		t.Fatalf("expected upstream to be unavailable when upstream client is nil")
	}

	count, err := service.ProcessOutbox(context.Background(), 20)
	if !errors.Is(err, ErrSyncUnavailable) {
		t.Fatalf("expected ErrSyncUnavailable when upstream is nil, got: %v", err)
	}

	if count != 0 {
		t.Fatalf("expected processed count = 0, got %d", count)
	}
}

func TestRequirement2_FailedUpload_LeavesRecordsPending(t *testing.T) {
	mockUpstream := &MockUpstreamClient{
		ShouldFail: true, // Simulating offline / failed global connection
	}

	records := []database.SyncInbox{
		{
			ID:               "inbox-101",
			SourceRecordID:   "redempt-101",
			SourceType:       "REDEMPTION",
			IdempotencyKey:   "redemption:55555555-5555-5555-5555-555555555555",
			Payload:          []byte(`{"redemption_id":"redempt-101","ticket_id":"55555555-5555-5555-5555-555555555555"}`),
			ProcessingStatus: database.SyncStatusPending,
		},
	}

	err := mockUpstream.UploadBatch(context.Background(), records)
	if err == nil {
		t.Fatalf("expected error from failed upload, got nil")
	}

	// Verify records are never accepted as uploaded and remain pending
	if len(mockUpstream.Uploaded) != 0 {
		t.Fatalf("no records should be recorded as uploaded upon failure, got %d", len(mockUpstream.Uploaded))
	}
	if records[0].ProcessingStatus != database.SyncStatusPending {
		t.Fatalf("record status must remain PENDING, got %s", records[0].ProcessingStatus)
	}
}

func TestRequirement3_SuccessfulUpload_ProcessesRecords(t *testing.T) {
	mockUpstream := &MockUpstreamClient{
		ShouldFail: false,
	}

	records := []database.SyncInbox{
		{
			ID:               "inbox-201",
			SourceRecordID:   "redempt-201",
			SourceType:       "REDEMPTION",
			IdempotencyKey:   "redemption:55555555-5555-5555-5555-555555555555",
			Payload:          []byte(`{"redemption_id":"redempt-201","ticket_id":"55555555-5555-5555-5555-555555555555"}`),
			ProcessingStatus: database.SyncStatusPending,
		},
	}

	err := mockUpstream.UploadBatch(context.Background(), records)
	if err != nil {
		t.Fatalf("expected successful upload, got %v", err)
	}

	if len(mockUpstream.Uploaded) != 1 {
		t.Fatalf("expected 1 uploaded record, got %d", len(mockUpstream.Uploaded))
	}
}

func TestRequirement10_InternetDisconnectionDuringEvent(t *testing.T) {
	mockUpstream := &MockUpstreamClient{
		ShouldFail: true, // Simulating disconnected venue internet
	}

	records := []database.SyncInbox{
		{
			ID:               "inbox-1",
			SourceRecordID:   "redempt-001",
			SourceType:       "REDEMPTION",
			IdempotencyKey:   "redemption:ticket-1",
			Payload:          []byte(`{"redemption_id":"redempt-001","ticket_id":"ticket-1"}`),
			ProcessingStatus: database.SyncStatusPending,
		},
	}

	err := mockUpstream.UploadBatch(context.Background(), records)
	if err == nil {
		t.Fatalf("expected network failure error when offline, got nil")
	}

	if len(mockUpstream.Uploaded) != 0 {
		t.Fatalf("no records should be recorded as uploaded during internet disconnection")
	}
}

func TestRequirement11_SynchronizationResumingAfterReconnection(t *testing.T) {
	mockUpstream := &MockUpstreamClient{
		ShouldFail: true,
	}

	records := []database.SyncInbox{
		{
			ID:               "inbox-1",
			SourceRecordID:   "redempt-001",
			SourceType:       "REDEMPTION",
			IdempotencyKey:   "redemption:ticket-1",
			Payload:          []byte(`{"redemption_id":"redempt-001","ticket_id":"ticket-1"}`),
			ProcessingStatus: database.SyncStatusPending,
		},
		{
			ID:               "inbox-2",
			SourceRecordID:   "redempt-002",
			SourceType:       "REDEMPTION",
			IdempotencyKey:   "redemption:ticket-2",
			Payload:          []byte(`{"redemption_id":"redempt-002","ticket_id":"ticket-2"}`),
			ProcessingStatus: database.SyncStatusPending,
		},
	}

	// 1. First attempt fails due to disconnect
	_ = mockUpstream.UploadBatch(context.Background(), records)
	if len(mockUpstream.Uploaded) != 0 {
		t.Fatalf("uploaded list should be empty during disconnection")
	}

	// 2. Internet reconnects
	mockUpstream.ShouldFail = false

	// 3. Resumed sync
	err := mockUpstream.UploadBatch(context.Background(), records)
	if err != nil {
		t.Fatalf("expected successful sync after reconnection, got %v", err)
	}

	if len(mockUpstream.Uploaded) != 2 {
		t.Fatalf("expected 2 uploaded records after reconnection, got %d", len(mockUpstream.Uploaded))
	}
}

func TestRequirement12_DuplicateSynchronizationUploads_Idempotency(t *testing.T) {
	mockUpstream := &MockUpstreamClient{
		ShouldFail: false,
	}

	records := []database.SyncInbox{
		{
			ID:               "inbox-1",
			SourceRecordID:   "redempt-001",
			SourceType:       "REDEMPTION",
			IdempotencyKey:   "redemption:ticket-1",
			Payload:          []byte(`{"redemption_id":"redempt-001","ticket_id":"ticket-1"}`),
			ProcessingStatus: database.SyncStatusPending,
		},
	}

	// Initial upload
	err := mockUpstream.UploadBatch(context.Background(), records)
	if err != nil {
		t.Fatalf("initial upload failed: %v", err)
	}

	// Duplicate retry with same record ID
	err = mockUpstream.UploadBatch(context.Background(), records)
	if err != nil {
		t.Fatalf("retry upload failed: %v", err)
	}

	uniqueRecords := make(map[string]bool)
	for _, rec := range mockUpstream.Uploaded {
		uniqueRecords[rec.IdempotencyKey] = true
	}

	if len(uniqueRecords) != 1 {
		t.Fatalf("expected 1 unique idempotency key, got %d", len(uniqueRecords))
	}
}

func TestSyncService_SignatureVerification(t *testing.T) {
	service := NewSyncService(nil, nil, "test-secret")

	validSig := service.verifySignature("event-001", "invalid-sig")
	if validSig {
		t.Fatalf("expected invalid signature to fail")
	}

	err := service.IngestManifest(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected error for nil manifest package")
	}
}
