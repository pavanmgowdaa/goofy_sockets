package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
)

type DemoTicketCredential struct {
	Index          int    `json:"index"`
	TicketID       string `json:"ticket_id"`
	AttendeeName   string `json:"attendee_name"`
	AttendeeEmail  string `json:"attendee_email"`
	TicketHash     string `json:"ticket_hash"`
	QRToken        string `json:"qr_token"`
	HMACSignature  string `json:"hmac_signature"`
	AssignedGateID string `json:"assigned_gate_id"`
	GateCode       string `json:"gate_code"`
	ValidUntil     string `json:"valid_until"`
}

type DemoBundle struct {
	OrganizerID   string                 `json:"organizer_id"`
	OrganizerName string                 `json:"organizer_name"`
	OrganizerMail string                 `json:"organizer_email"`
	EventID       string                 `json:"event_id"`
	EventName     string                 `json:"event_name"`
	Gates         []map[string]string    `json:"gates"`
	TotalTickets  int                    `json:"total_tickets"`
	SecretKey     string                 `json:"secret_key"`
	Tickets       []DemoTicketCredential `json:"tickets"`
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres@127.0.0.1:5432/fairpass_mirror?sslmode=disable"
	}

	secretKey := os.Getenv("FAIRPASS_SECRET_KEY")
	if secretKey == "" {
		secretKey = "fairpass-venue-offline-secret-key"
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database %s: %v", dsn, err)
	}
	defer conn.Close(ctx)

	tx, err := conn.Begin(ctx)
	if err != nil {
		log.Fatalf("Failed to begin transaction: %v", err)
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC()
	start := now.Add(-1 * time.Hour)
	end := now.Add(72 * time.Hour)

	log.Println("Seeding FAIRPASS database (fairpass_mirror) with REAL dummy data (NO TWIN)...")

	// 1. Organizer
	organizerID := "a0000000-0000-0000-0000-000000000001"
	organizerName := "FAIRPASS Hackathon Organizer"
	organizerEmail := "test-organizer-hackathon@fairpass.internal"

	_, err = tx.Exec(ctx, `
		INSERT INTO public.users (id, full_name, email, password_hash, role, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'ORGANIZER', $5, $5)
		ON CONFLICT (id) DO UPDATE SET full_name = EXCLUDED.full_name, email = EXCLUDED.email;
	`, organizerID, organizerName, organizerEmail, "hash_organizer_sec123", now)
	if err != nil {
		log.Fatalf("Failed to insert organizer: %v", err)
	}

	// 2. Event
	eventID := "b0000000-0000-0000-0000-000000000001"
	eventName := "[TEST-BENCH] FAIRPASS Offline Verification Event"
	venueName := "Hackathon Main Arena"

	_, err = tx.Exec(ctx, `
		INSERT INTO public.events (id, organizer_id, event_name, description, venue_name, event_start, event_end, total_capacity, event_status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 100, 'PUBLISHED', $8, $8)
		ON CONFLICT (id) DO UPDATE SET event_name = EXCLUDED.event_name, event_start = EXCLUDED.event_start, event_end = EXCLUDED.event_end;
	`, eventID, organizerID, eventName, "Official 24-Hour Hackathon Dedicated Test Event", venueName, start, end, now)
	if err != nil {
		log.Fatalf("Failed to insert event: %v", err)
	}

	// 3. Venue Gates (GATE-01 and GATE-02)
	gate1ID := "c0000000-0000-0000-0000-000000000001"
	gate2ID := "c0000000-0000-0000-0000-000000000002"

	gates := []struct {
		ID     string
		Code   string
		Name   string
		Secret string
	}{
		{ID: gate1ID, Code: "GATE-01", Name: "North Entry Gate 1", Secret: "secret-gate-01"},
		{ID: gate2ID, Code: "GATE-02", Name: "South Entry Gate 2", Secret: "secret-gate-02"},
	}

	for idx, g := range gates {
		_, err = tx.Exec(ctx, `
			INSERT INTO public.venue_gates (id, event_id, device_identifier, gate_name, device_secret_hash, device_status, created_at)
			VALUES ($1, $2, $3, $4, $5, 'ACTIVE', $6)
			ON CONFLICT (device_identifier) DO UPDATE 
			SET gate_name = EXCLUDED.gate_name, device_secret_hash = EXCLUDED.device_secret_hash, event_id = EXCLUDED.event_id;
		`, g.ID, eventID, g.Code, g.Name, g.Secret, now)
		if err != nil {
			log.Fatalf("Failed to insert venue gate %s: %v", g.Code, err)
		}

		// Device Credentials
		credID := fmt.Sprintf("d0000000-0000-0000-0000-%012d", idx+1)
		_, err = tx.Exec(ctx, `
			INSERT INTO public.device_credentials (id, device_identifier, gate_id, public_key, credential_signature, issuer_key_id, issued_at, expires_at, created_at)
			VALUES ($1, $2, $3, $4, $5, 'fairpass-key-issuer-01', $6, $7, $6)
			ON CONFLICT (device_identifier) DO UPDATE 
			SET expires_at = EXCLUDED.expires_at, gate_id = EXCLUDED.gate_id;
		`, credID, g.Code, g.ID, "ed25519-key-"+g.Code, "sig-"+g.Code, start, end)
		if err != nil {
			log.Fatalf("Failed to insert device credential for %s: %v", g.Code, err)
		}
	}

	// 4. 30 Demo Attendee Accounts and 30 Zero-Price Tickets
	var ticketCredentials []DemoTicketCredential

	for i := 1; i <= 30; i++ {
		memberID := fmt.Sprintf("e0000000-0000-0000-0000-%012d", i)
		memberName := fmt.Sprintf("Demo Member %02d", i)
		memberEmail := fmt.Sprintf("demo-member-%02d@fairpass.internal", i)

		_, err = tx.Exec(ctx, `
			INSERT INTO public.users (id, full_name, email, password_hash, role, created_at, updated_at)
			VALUES ($1, $2, $3, 'hash_member_pass123', 'CUSTOMER', $4, $4)
			ON CONFLICT (id) DO UPDATE SET email = EXCLUDED.email;
		`, memberID, memberName, memberEmail, now)
		if err != nil {
			log.Fatalf("Failed to insert attendee %s: %v", memberEmail, err)
		}

		ticketID := fmt.Sprintf("f0000000-0000-0000-0000-%012d", i)
		qrToken := fmt.Sprintf("token-demo-ticket-%02d-fairpass-secure", i)

		// Compute SHA-256 hash of token (64 hex characters)
		tokenSum := sha256.Sum256([]byte(qrToken))
		ticketHash := hex.EncodeToString(tokenSum[:])

		// Compute HMAC-SHA256 signature
		mac := hmac.New(sha256.New, []byte(secretKey))
		mac.Write([]byte(ticketID + ":" + eventID))
		sig := hex.EncodeToString(mac.Sum(nil))

		// Alternate gate allocation
		assignedGate := gate1ID
		assignedGateCode := "GATE-01"
		if i%2 == 0 {
			assignedGate = gate2ID
			assignedGateCode = "GATE-02"
		}

		blockchainToken := fmt.Sprintf("QUAI-DEMO-TKT-%02d", i)

		_, err = tx.Exec(ctx, `
			INSERT INTO public.tickets (
				id, event_id, ticket_hash, original_owner_id, current_owner_id,
				ticket_type, price, ticket_status, valid_until, blockchain_token_id, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $4, 'Standard Admission', 0.00, 'ACTIVE', $5, $6, $7, $7)
			ON CONFLICT (ticket_hash) DO UPDATE 
			SET ticket_status = 'ACTIVE', valid_until = EXCLUDED.valid_until;
		`, ticketID, eventID, ticketHash, memberID, end, blockchainToken, now)
		if err != nil {
			log.Fatalf("Failed to insert ticket %s: %v", ticketID, err)
		}

		ticketCredentials = append(ticketCredentials, DemoTicketCredential{
			Index:          i,
			TicketID:       ticketID,
			AttendeeName:   memberName,
			AttendeeEmail:  memberEmail,
			TicketHash:     ticketHash,
			QRToken:        qrToken,
			HMACSignature:  sig,
			AssignedGateID: assignedGate,
			GateCode:       assignedGateCode,
			ValidUntil:     end.Format(time.RFC3339),
		})
	}

	// 5. Manifest Metadata
	manifestMetaID := "a1111111-0000-0000-0000-000000000001"
	manifestHashPayload := fmt.Sprintf("%s:%d:%d", eventID, 1, 30)
	manifestHashSum := sha256.Sum256([]byte(manifestHashPayload))
	manifestHash := hex.EncodeToString(manifestHashSum[:])

	manifestMac := hmac.New(sha256.New, []byte(secretKey))
	manifestMac.Write([]byte(eventID + ":manifest"))
	manifestSig := hex.EncodeToString(manifestMac.Sum(nil))

	_, err = tx.Exec(ctx, `
		INSERT INTO public.manifest_metadata (
			id, event_id, manifest_version, manifest_hash, signature, signer_key_id, issued_at, expires_at, created_at
		)
		VALUES ($1, $2, 1, $3, $4, 'fairpass-manifest-signer', $5, $6, $5)
		ON CONFLICT (event_id, manifest_version) DO UPDATE 
		SET manifest_hash = EXCLUDED.manifest_hash, signature = EXCLUDED.signature, expires_at = EXCLUDED.expires_at;
	`, manifestMetaID, eventID, manifestHash, manifestSig, now, end)
	if err != nil {
		log.Fatalf("Failed to insert manifest metadata: %v", err)
	}

	if err = tx.Commit(ctx); err != nil {
		log.Fatalf("Failed to commit transaction: %v", err)
	}

	log.Println("✓ Transaction committed successfully! All 30 demo members, event, gates, and tickets pushed to fairpass_mirror.")

	// Export demo credentials bundle JSON
	bundle := DemoBundle{
		OrganizerID:   organizerID,
		OrganizerName: organizerName,
		OrganizerMail: organizerEmail,
		EventID:       eventID,
		EventName:     eventName,
		Gates: []map[string]string{
			{"id": gate1ID, "code": "GATE-01", "name": "North Entry Gate 1", "secret": "secret-gate-01"},
			{"id": gate2ID, "code": "GATE-02", "name": "South Entry Gate 2", "secret": "secret-gate-02"},
		},
		TotalTickets: len(ticketCredentials),
		SecretKey:    secretKey,
		Tickets:      ticketCredentials,
	}

	jsonBytes, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal demo bundle: %v", err)
	}

	bundlePath := "D:\\goofy_sockets\\demo_members_and_tickets.json"
	if err = os.WriteFile(bundlePath, jsonBytes, 0644); err != nil {
		log.Printf("Warning: could not write %s: %v", bundlePath, err)
	} else {
		log.Printf("✓ Exported demo credentials to: %s", bundlePath)
	}

	fmt.Println("\n================================================================================")
	fmt.Println("             FAIRPASS REAL DATABASE SEEDING COMPLETE (NO TWIN)")
	fmt.Println("================================================================================")
	fmt.Printf("Database Target      : %s\n", dsn)
	fmt.Printf("Event Created        : %s (%s)\n", eventName, eventID)
	fmt.Printf("Organizer Created    : %s (%s)\n", organizerName, organizerEmail)
	fmt.Printf("Gates Created        : 2 (GATE-01, GATE-02)\n")
	fmt.Printf("Device Credentials   : 2 registered\n")
	fmt.Printf("Attendee Accounts    : 30 created (demo-member-01 to demo-member-30)\n")
	fmt.Printf("Tickets Issued       : 30 zero-price tickets with unique hashes\n")
	fmt.Printf("Manifest Signed      : Version 1 (HMAC-SHA256 signature valid)\n")
	fmt.Println("================================================================================")
}
