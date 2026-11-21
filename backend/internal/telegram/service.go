package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidToken = errors.New("linking token is invalid or expired")
	ErrTokenExpired = errors.New("linking token has expired")
)

// BookingDetails holds information needed to construct the Telegram notification message.
type BookingDetails struct {
	BookingID    string
	EventTitle   string
	TicketCount  int
	GateCodes    []string
	StartsAt     time.Time
	EndsAt       time.Time
	AttendeeName string
}

// Service manages Telegram notifications and user account linking.
type Service struct {
	client        Client
	db            *pgxpool.Pool
	botUsername   string
	defaultChatID int64
}

// NewService creates a new Telegram notification Service.
func NewService(client Client, db *pgxpool.Pool, botUsername string, defaultChatID int64) *Service {
	return &Service{
		client:        client,
		db:            db,
		botUsername:   botUsername,
		defaultChatID: defaultChatID,
	}
}

// GenerateLinkingToken creates a cryptographically secure, short-lived single-use linking token.
func (s *Service) GenerateLinkingToken(ctx context.Context, userID string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}

	rawBytes := make([]byte, 24)
	if _, err := rand.Read(rawBytes); err != nil {
		return "", fmt.Errorf("generate random linking token: %w", err)
	}
	token := hex.EncodeToString(rawBytes)
	expiresAt := time.Now().UTC().Add(ttl)

	if s.db != nil {
		_, err := s.db.Exec(ctx, `
			INSERT INTO telegram_link_tokens (token, user_id, expires_at)
			VALUES ($1, $2, $3)
		`, token, userID, expiresAt)
		if err != nil {
			return "", fmt.Errorf("store telegram linking token: %w", err)
		}
	}

	return token, nil
}

// GenerateDeepLink returns the full Telegram deep link URL for a linking token.
func (s *Service) GenerateDeepLink(token string) string {
	bot := s.botUsername
	if bot == "" {
		bot = "FAIRPASSBot"
	}
	return fmt.Sprintf("https://t.me/%s?start=%s", bot, token)
}

// LinkAccount links a Telegram chat ID to a FAIRPASS user using an unexpired linking token.
// The linking token is consumed upon successful association (single-use).
func (s *Service) LinkAccount(ctx context.Context, token string, chatID int64, username string) (string, error) {
	if strings.TrimSpace(token) == "" {
		return "", ErrInvalidToken
	}

	if s.db == nil {
		return "", errors.New("database pool not initialized")
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin link transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var userID string
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT user_id, expires_at 
		FROM telegram_link_tokens 
		WHERE token = $1 
		FOR UPDATE
	`, token).Scan(&userID, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrInvalidToken
		}
		return "", fmt.Errorf("query linking token: %w", err)
	}

	if time.Now().UTC().After(expiresAt) {
		_, _ = tx.Exec(ctx, `DELETE FROM telegram_link_tokens WHERE token = $1`, token)
		_ = tx.Commit(ctx)
		return "", ErrTokenExpired
	}

	// Delete consumed single-use token
	_, err = tx.Exec(ctx, `DELETE FROM telegram_link_tokens WHERE token = $1`, token)
	if err != nil {
		return "", fmt.Errorf("consume token: %w", err)
	}

	// Upsert into telegram_links
	_, err = tx.Exec(ctx, `
		INSERT INTO telegram_links (user_id, chat_id, username, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (user_id) DO UPDATE 
		SET chat_id = EXCLUDED.chat_id, username = EXCLUDED.username, updated_at = now()
	`, userID, chatID, username)
	if err != nil {
		return "", fmt.Errorf("associate telegram account: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit linking: %w", err)
	}

	return userID, nil
}

// FormatBookingMessage formats the Telegram booking notification HTML message.
func FormatBookingMessage(d BookingDetails) string {
	gatesStr := "Standard Gate"
	if len(d.GateCodes) > 0 {
		gatesStr = strings.Join(d.GateCodes, ", ")
	}

	dateFmt := "02 Jan 2006, 15:04 MST"
	startStr := d.StartsAt.Format(dateFmt)
	endStr := d.EndsAt.Format(dateFmt)

	nameGreeting := "Valued Guest"
	if strings.TrimSpace(d.AttendeeName) != "" {
		nameGreeting = d.AttendeeName
	}

	return fmt.Sprintf(
		"🎟️ <b>FAIRPASS BOOKING CONFIRMATION</b>\n\n"+
			"Hello <b>%s</b>,\n"+
			"Your tickets for <b>%s</b> are confirmed and issued!\n\n"+
			"📋 <b>Booking Reference:</b> <code>%s</code>\n"+
			"🎫 <b>Ticket Quantity:</b> %d\n"+
			"🚪 <b>Assigned Gate(s):</b> <b>%s</b>\n"+
			"🕒 <b>Event Starts:</b> %s\n"+
			"🏁 <b>Event Ends:</b> %s\n\n"+
			"⚡ <i>Your secure offline QR code has been generated. Please arrive at your allocated gate for scanning.</i>\n\n"+
			"🔒 <i>Secured by FAIRPASS Cryptographic Verification</i>",
		escapeHTML(nameGreeting),
		escapeHTML(d.EventTitle),
		escapeHTML(d.BookingID),
		d.TicketCount,
		escapeHTML(gatesStr),
		escapeHTML(startStr),
		escapeHTML(endStr),
	)
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// NotifyBookingConfirmed asynchronously delivers a booking confirmation notification to the user's
// linked Telegram chat (or default test chat if configured).
// This method handles all errors gracefully: it NEVER panics, blocks, or causes data failure.
func (s *Service) NotifyBookingConfirmed(ctx context.Context, bookingID string) {
	if s == nil || s.client == nil {
		return
	}

	// Run with detached timeout context
	notifyCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if s.db == nil {
		log.Printf("[TELEGRAM] Database unavailable; skipping notification for booking %s", bookingID)
		return
	}

	// 1. Fetch booking, user, event, and ticket gate info
	var (
		userID       string
		attendeeName string
		eventTitle   string
		startsAt     time.Time
		endsAt       time.Time
	)

	err := s.db.QueryRow(notifyCtx, `
		SELECT b.user_id, u.name, ev.title, ev.starts_at, ev.ends_at
		FROM bookings b
		JOIN users u ON u.id = b.user_id
		JOIN events ev ON ev.id = b.event_id
		WHERE b.id = $1
	`, bookingID).Scan(&userID, &attendeeName, &eventTitle, &startsAt, &endsAt)
	if err != nil {
		log.Printf("[TELEGRAM] Could not load booking %s details: %v", bookingID, err)
		return
	}

	// 2. Query ticket count and allocated gate codes
	gateRows, err := s.db.Query(notifyCtx, `
		SELECT COALESCE(eg.gate_code, 'UNASSIGNED')
		FROM tickets t
		LEFT JOIN event_gates eg ON eg.id = t.assigned_gate_id
		WHERE t.booking_id = $1
	`, bookingID)
	if err != nil {
		log.Printf("[TELEGRAM] Could not load tickets for booking %s: %v", bookingID, err)
		return
	}
	defer gateRows.Close()

	gateSet := make(map[string]struct{})
	ticketCount := 0
	for gateRows.Next() {
		var code string
		if err := gateRows.Scan(&code); err == nil {
			ticketCount++
			if code != "UNASSIGNED" {
				gateSet[code] = struct{}{}
			}
		}
	}

	gates := make([]string, 0, len(gateSet))
	for g := range gateSet {
		gates = append(gates, g)
	}

	// 3. Resolve destination chat ID: check user's linked account first
	var chatID int64
	err = s.db.QueryRow(notifyCtx, `
		SELECT chat_id FROM telegram_links WHERE user_id = $1
	`, userID).Scan(&chatID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// User has not linked Telegram account
			if s.defaultChatID != 0 {
				log.Printf("[TELEGRAM] User %s has no linked Telegram chat; delivering to default test chat %d", userID, s.defaultChatID)
				chatID = s.defaultChatID
			} else {
				log.Printf("[TELEGRAM] User %s has no linked Telegram chat; notification skipped", userID)
				return
			}
		} else {
			log.Printf("[TELEGRAM] Query telegram_links error for user %s: %v", userID, err)
			return
		}
	}

	// 4. Format and deliver message
	details := BookingDetails{
		BookingID:    bookingID,
		EventTitle:   eventTitle,
		TicketCount:  ticketCount,
		GateCodes:    gates,
		StartsAt:     startsAt,
		EndsAt:       endsAt,
		AttendeeName: attendeeName,
	}

	messageText := FormatBookingMessage(details)

	if sendErr := s.client.SendMessage(notifyCtx, chatID, messageText); sendErr != nil {
		log.Printf("[TELEGRAM ERROR] Failed to send Telegram notification to chat %d for booking %s: %v", chatID, bookingID, sendErr)
		return
	}

	log.Printf("[TELEGRAM] Successfully delivered booking confirmation to chat %d for booking %s", chatID, bookingID)
}
