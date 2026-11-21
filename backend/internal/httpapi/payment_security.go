package httpapi

import (
	"errors"
	"fmt"
)

var (
	// ErrAlreadyPaid is returned when attempting to confirm an already paid booking.
	ErrAlreadyPaid = errors.New("booking is already paid")

	// ErrUnauthorizedBookingConfirmation is returned when a caller attempts to confirm another user's booking.
	ErrUnauthorizedBookingConfirmation = errors.New("forbidden: caller is not authorized to confirm this booking")

	// ErrPaymentRequired is returned when direct confirmation is attempted on a non-zero-price booking.
	ErrPaymentRequired = errors.New("payment required: non-zero bookings must be paid via payment provider")

	// ErrInvalidBookingStatus is returned when a booking is not in 'pending' status.
	ErrInvalidBookingStatus = errors.New("invalid booking status for confirmation")
)

// ValidateBookingConfirmation validates the security invariants for direct booking confirmation:
// 1. If status is already 'paid', returns ErrAlreadyPaid for idempotent handling.
// 2. Booking status must be 'pending'.
// 3. Caller must be the owner of the booking or have the 'administrator' role.
// 4. Total amount (totalMinor) must be exactly 0. Direct confirmation is ONLY permitted for zero-price bookings.
//
// Non-zero bookings MUST be paid and confirmed exclusively via the payment provider webhook.
func ValidateBookingConfirmation(bookingOwnerID, callerUserID, callerRole string, totalMinor int64, currentStatus string) error {
	if currentStatus == "paid" {
		return ErrAlreadyPaid
	}
	if currentStatus != "pending" {
		return fmt.Errorf("%w: cannot confirm booking with status %q", ErrInvalidBookingStatus, currentStatus)
	}
	if callerRole != "administrator" && callerUserID != bookingOwnerID {
		return ErrUnauthorizedBookingConfirmation
	}
	if totalMinor > 0 {
		return ErrPaymentRequired
	}
	return nil
}
