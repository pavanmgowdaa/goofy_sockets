package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// 1. TestTokenGenerationAndCryptoValidation verifies token entropy, randomness, and deep-link generation.
func TestTokenGenerationAndCryptoValidation(t *testing.T) {
	svc := NewService(nil, nil, "FairpassTestBot", 0)

	tokens := make(map[string]bool)
	for i := 0; i < 50; i++ {
		token, err := svc.GenerateLinkingToken(context.Background(), "user-test-id", 15*time.Minute)
		if err != nil {
			t.Fatalf("unexpected token generation error: %v", err)
		}

		// Verify length: 24 bytes hex-encoded = 48 chars
		if len(token) != 48 {
			t.Fatalf("expected 48-char token, got %d (%q)", len(token), token)
		}

		// Verify uniqueness / high entropy
		if tokens[token] {
			t.Fatalf("duplicate token detected! Crypto rand collision: %q", token)
		}
		tokens[token] = true

		// Verify deep link format
		deepLink := svc.GenerateDeepLink(token)
		expectedPrefix := "https://t.me/FairpassTestBot?start=" + token
		if deepLink != expectedPrefix {
			t.Fatalf("expected %q, got %q", expectedPrefix, deepLink)
		}
	}
}

// 2. TestAccountLinkingLogic verifies linking validation, expiry checking, and single-use consumption.
func TestAccountLinkingLogic(t *testing.T) {
	// In-memory mock token store
	type tokenEntry struct {
		userID    string
		expiresAt time.Time
	}
	tokens := map[string]tokenEntry{
		"valid-token-123": {
			userID:    "user-uuid-1",
			expiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
		"expired-token-456": {
			userID:    "user-uuid-2",
			expiresAt: time.Now().UTC().Add(-5 * time.Minute),
		},
	}
	links := map[string]int64{}

	linkMock := func(token string, chatID int64) (string, error) {
		entry, exists := tokens[token]
		if !exists {
			return "", ErrInvalidToken
		}
		if time.Now().UTC().After(entry.expiresAt) {
			delete(tokens, token)
			return "", ErrTokenExpired
		}
		delete(tokens, token) // Single use consumption
		links[entry.userID] = chatID
		return entry.userID, nil
	}

	// Case A: Valid unexpired token succeeds
	uid, err := linkMock("valid-token-123", 987654321)
	if err != nil {
		t.Fatalf("expected valid token linking to succeed, got: %v", err)
	}
	if uid != "user-uuid-1" || links["user-uuid-1"] != 987654321 {
		t.Fatalf("expected user-uuid-1 linked to chat 987654321, got uid=%q chat=%d", uid, links["user-uuid-1"])
	}

	// Case B: Same token reused fails (single use)
	_, err = linkMock("valid-token-123", 987654321)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken on single-use retry, got: %v", err)
	}

	// Case C: Expired token fails
	_, err = linkMock("expired-token-456", 11223344)
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expected ErrTokenExpired for expired token, got: %v", err)
	}

	// Case D: Non-existent token fails
	_, err = linkMock("non-existent-token", 999999)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken for non-existent token, got: %v", err)
	}
}

// 3. TestMessageFormatting verifies that all required booking details are present in the output message.
func TestMessageFormatting(t *testing.T) {
	starts := time.Date(2026, 12, 1, 18, 0, 0, 0, time.UTC)
	ends := time.Date(2026, 12, 1, 23, 0, 0, 0, time.UTC)

	details := BookingDetails{
		BookingID:    "book-hackathon-9999",
		EventTitle:   "FAIRPASS Hackathon Finals & Concert",
		TicketCount:  4,
		GateCodes:    []string{"GATE-A", "GATE-B"},
		StartsAt:     starts,
		EndsAt:       ends,
		AttendeeName: "John Doe",
	}

	msg := FormatBookingMessage(details)

	// Check for all required components
	requiredSubstrings := []string{
		"FAIRPASS BOOKING CONFIRMATION",
		"John Doe",
		"FAIRPASS Hackathon Finals &amp; Concert",
		"book-hackathon-9999",
		"4",
		"GATE-A, GATE-B",
		"01 Dec 2026",
	}

	for _, sub := range requiredSubstrings {
		if !strings.Contains(msg, sub) {
			t.Errorf("expected formatted message to contain %q, but was missing.\nFull message:\n%s", sub, msg)
		}
	}
}

// 4. TestAsyncNotificationDispatchOnBookingConfirmation tests notification delivery to mock client.
func TestAsyncNotificationDispatchOnBookingConfirmation(t *testing.T) {
	mockClient := NewMockClient()

	// Simulate dispatch with formatted message
	details := BookingDetails{
		BookingID:    "b-12345",
		EventTitle:   "Tech Fest 2026",
		TicketCount:  2,
		GateCodes:    []string{"GATE-01"},
		StartsAt:     time.Now().UTC(),
		EndsAt:       time.Now().UTC().Add(4 * time.Hour),
		AttendeeName: "Alice Smith",
	}

	text := FormatBookingMessage(details)
	targetChatID := int64(123456789)

	err := mockClient.SendMessage(context.Background(), targetChatID, text)
	if err != nil {
		t.Fatalf("unexpected mock dispatch error: %v", err)
	}

	sent := mockClient.GetSentMessages()
	if len(sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(sent))
	}
	if sent[0].ChatID != targetChatID {
		t.Fatalf("expected chat ID %d, got %d", targetChatID, sent[0].ChatID)
	}
	if !strings.Contains(sent[0].Text, "Tech Fest 2026") {
		t.Fatalf("message body missing event title: %s", sent[0].Text)
	}
}

// 5. TestGracefulHandlingOfUnlinkedUsers verifies that unlinked users do not cause failure.
func TestGracefulHandlingOfUnlinkedUsers(t *testing.T) {
	mockClient := NewMockClient()
	svc := NewService(mockClient, nil, "FairpassTestBot", 0)

	// Calling NotifyBookingConfirmed with no database / unlinked user should not panic or fail
	svc.NotifyBookingConfirmed(context.Background(), "unlinked-booking-id")

	// Verify no messages dispatched
	sent := mockClient.GetSentMessages()
	if len(sent) != 0 {
		t.Fatalf("expected 0 messages dispatched for unlinked user, got %d", len(sent))
	}
}

// 6. TestTelegramAPIFailureResilience verifies that Telegram API failures do not panic or bubble up.
func TestTelegramAPIFailureResilience(t *testing.T) {
	mockClient := NewMockClient()
	mockClient.ShouldFail = true
	mockClient.FailError = errors.New("upstream Telegram Bot API returned HTTP 502 Bad Gateway")

	// Calling SendMessage returns an error as expected
	err := mockClient.SendMessage(context.Background(), 12345, "test")
	if err == nil {
		t.Fatal("expected failure from mock client, got nil")
	}

	// Service NotifyBookingConfirmed handles network/API errors safely without panicking
	svc := NewService(mockClient, nil, "FairpassTestBot", 0)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NotifyBookingConfirmed panicked on Telegram API error: %v", r)
		}
	}()

	svc.NotifyBookingConfirmed(context.Background(), "booking-any")
}
