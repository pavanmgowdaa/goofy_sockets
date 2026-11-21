package httpapi

import (
	"errors"
	"testing"
)

// TestPaymentSecurity_ZeroPriceBookingConfirmedByOwner verifies that a zero-price booking
// (total_minor == 0) can be directly confirmed by its legitimate owner.
func TestPaymentSecurity_ZeroPriceBookingConfirmedByOwner(t *testing.T) {
	ownerID := "owner-uuid-1111-2222-3333"
	callerID := "owner-uuid-1111-2222-3333"
	callerRole := "attendee"
	var totalMinor int64 = 0
	status := "pending"

	err := ValidateBookingConfirmation(ownerID, callerID, callerRole, totalMinor, status)
	if err != nil {
		t.Fatalf("expected zero-price booking confirmation by owner to succeed, got: %v", err)
	}
}

// TestPaymentSecurity_NonZeroBookingRejectedWithoutPayment verifies that direct confirmation
// is strictly rejected for any booking with total_minor > 0 (must go through payment provider).
func TestPaymentSecurity_NonZeroBookingRejectedWithoutPayment(t *testing.T) {
	testAmounts := []int64{1, 100, 49900, 100000}
	ownerID := "owner-uuid-1111-2222-3333"
	callerID := "owner-uuid-1111-2222-3333"
	callerRole := "attendee"
	status := "pending"

	for _, amount := range testAmounts {
		err := ValidateBookingConfirmation(ownerID, callerID, callerRole, amount, status)
		if err == nil {
			t.Fatalf("expected non-zero booking (%d minor) to be rejected without payment, but it succeeded", amount)
		}
		if !errors.Is(err, ErrPaymentRequired) {
			t.Fatalf("expected ErrPaymentRequired for amount %d, got: %v", amount, err)
		}
	}
}

// TestPaymentSecurity_UnauthorizedUserRejected verifies that an attacker/unauthorized caller
// cannot confirm another user's booking, even if the price is 0.
func TestPaymentSecurity_UnauthorizedUserRejected(t *testing.T) {
	ownerID := "legitimate-owner-1111"
	attackerID := "attacker-user-9999"
	callerRole := "attendee"
	var totalMinor int64 = 0
	status := "pending"

	err := ValidateBookingConfirmation(ownerID, attackerID, callerRole, totalMinor, status)
	if err == nil {
		t.Fatal("expected unauthorized confirmation to be rejected, but it succeeded")
	}
	if !errors.Is(err, ErrUnauthorizedBookingConfirmation) {
		t.Fatalf("expected ErrUnauthorizedBookingConfirmation, got: %v", err)
	}
}

// TestPaymentSecurity_AdministratorAllowedForZeroPrice verifies that a user with the 'administrator'
// role is authorized to confirm a zero-price booking on behalf of another user.
func TestPaymentSecurity_AdministratorAllowedForZeroPrice(t *testing.T) {
	ownerID := "attendee-user-1111"
	adminID := "admin-user-0000"
	callerRole := "administrator"
	var totalMinor int64 = 0
	status := "pending"

	err := ValidateBookingConfirmation(ownerID, adminID, callerRole, totalMinor, status)
	if err != nil {
		t.Fatalf("expected administrator to be allowed to confirm zero-price booking, got: %v", err)
	}

	// However, even an administrator cannot bypass payment for non-zero amounts
	err = ValidateBookingConfirmation(ownerID, adminID, callerRole, 5000, status)
	if err == nil || !errors.Is(err, ErrPaymentRequired) {
		t.Fatalf("expected administrator to still be blocked from confirming non-zero booking directly, got: %v", err)
	}
}

// TestPaymentSecurity_IdempotentAlreadyPaid verifies that re-confirming an already paid booking
// returns ErrAlreadyPaid so the handler can return HTTP 200 already_paid idempotently.
func TestPaymentSecurity_IdempotentAlreadyPaid(t *testing.T) {
	ownerID := "owner-uuid-1111-2222-3333"
	callerID := "owner-uuid-1111-2222-3333"
	callerRole := "attendee"
	var totalMinor int64 = 0
	status := "paid"

	err := ValidateBookingConfirmation(ownerID, callerID, callerRole, totalMinor, status)
	if err == nil {
		t.Fatal("expected ErrAlreadyPaid, got nil")
	}
	if !errors.Is(err, ErrAlreadyPaid) {
		t.Fatalf("expected ErrAlreadyPaid, got: %v", err)
	}
}

// TestPaymentSecurity_InvalidOrCancelledStatus verifies that bookings in 'cancelled', 'failed',
// or 'refunded' status cannot be confirmed.
func TestPaymentSecurity_InvalidOrCancelledStatus(t *testing.T) {
	ownerID := "owner-uuid-1111-2222-3333"
	callerID := "owner-uuid-1111-2222-3333"
	callerRole := "attendee"
	var totalMinor int64 = 0

	invalidStatuses := []string{"cancelled", "failed", "refunded", "unknown"}
	for _, st := range invalidStatuses {
		err := ValidateBookingConfirmation(ownerID, callerID, callerRole, totalMinor, st)
		if err == nil {
			t.Fatalf("expected status %q to be rejected, but it succeeded", st)
		}
		if !errors.Is(err, ErrInvalidBookingStatus) {
			t.Fatalf("expected ErrInvalidBookingStatus for status %q, got: %v", st, err)
		}
	}
}
