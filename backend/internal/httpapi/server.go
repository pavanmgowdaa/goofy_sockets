package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fairpass/backend/internal/auth"
	"github.com/fairpass/backend/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	cfg config.Config
	db  *pgxpool.Pool
	mux *http.ServeMux
}

var rateMu sync.Mutex
var rateWindows = map[string]struct {
	start time.Time
	count int
}{}

func New(c config.Config, p *pgxpool.Pool) http.Handler {
	s := &Server{cfg: c, db: p, mux: http.NewServeMux()}
	s.routes()
	return s.recover(s.limit(s.mux))
}
func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("POST /v1/auth/register", s.register)
	s.mux.HandleFunc("POST /v1/auth/login", s.login)
	s.mux.HandleFunc("POST /v1/auth/refresh", s.refresh)
	s.mux.Handle("GET /v1/me", s.protect(http.HandlerFunc(s.me)))
	s.mux.Handle("POST /v1/events", s.protect(http.HandlerFunc(s.createEvent)))
	s.mux.Handle("POST /v1/events/{id}/ticket-types", s.protect(http.HandlerFunc(s.createTicketType)))
	s.mux.HandleFunc("GET /v1/events", s.events)
	s.mux.HandleFunc("GET /v1/events/{id}/ticket-types", s.ticketTypes)
	s.mux.Handle("POST /v1/events/{id}/publish", s.protect(http.HandlerFunc(s.publishEvent)))
	s.mux.Handle("POST /v1/bookings", s.protect(http.HandlerFunc(s.book)))
	s.mux.Handle("POST /v1/bookings/{id}/payment-order", s.protect(http.HandlerFunc(s.paymentOrder)))
	s.mux.HandleFunc("POST /v1/payments/razorpay/webhook", s.webhook)
	s.mux.Handle("POST /v1/gate/verify", s.deviceAuth(http.HandlerFunc(s.verifyTicket)))
	s.mux.Handle("POST /v1/gate/readings", s.deviceAuth(http.HandlerFunc(s.readings)))
	s.mux.Handle("POST /v1/gate/heartbeat", s.deviceAuth(http.HandlerFunc(s.heartbeat)))
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, c := context.WithTimeout(r.Context(), 2*time.Second)
	defer c()
	if e := s.db.Ping(ctx); e != nil {
		writeError(w, 503, "database unavailable")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

type registerRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var q registerRequest
	if decode(r, &q) != nil || q.Name == "" || q.Email == "" {
		writeError(w, 400, "name, email and password are required")
		return
	}
	role := q.Role
	if role == "" {
		role = "attendee"
	}
	if role != "attendee" && role != "organizer" {
		writeError(w, 400, "self-registration only supports attendee or organizer")
		return
	}
	hash, e := auth.HashPassword(q.Password)
	if e != nil {
		writeError(w, 400, e.Error())
		return
	}
	var id string
	e = s.db.QueryRow(r.Context(), `INSERT INTO users(name,email,password_hash,role) VALUES($1,lower($2),$3,$4) RETURNING id`, q.Name, q.Email, hash, role).Scan(&id)
	if e != nil {
		writeError(w, 409, "email already registered")
		return
	}
	s.tokens(w, id, role)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var q loginRequest
	if decode(r, &q) != nil {
		writeError(w, 400, "invalid request")
		return
	}
	var id, hash, role string
	e := s.db.QueryRow(r.Context(), `SELECT id,password_hash,role FROM users WHERE email=lower($1) AND status='active'`, q.Email).Scan(&id, &hash, &role)
	if e != nil || !auth.CheckPassword(hash, q.Password) {
		writeError(w, 401, "invalid credentials")
		return
	}
	_, _ = s.db.Exec(r.Context(), `UPDATE users SET last_login_at=now() WHERE id=$1`, id)
	s.tokens(w, id, role)
}
func (s *Server) tokens(w http.ResponseWriter, id, role string) {
	a, e := auth.Token(s.cfg.JWTSecret, id, role, s.cfg.AccessTTL)
	if e != nil {
		writeError(w, 500, "token creation failed")
		return
	}
	raw, e := auth.RandomToken()
	if e != nil {
		writeError(w, 500, "token creation failed")
		return
	}
	_, e = s.db.Exec(context.Background(), `INSERT INTO refresh_tokens(user_id,token_hash,expires_at) VALUES($1,$2,now()+$3::interval)`, id, auth.SHA256Hex([]byte(raw)), s.cfg.RefreshTTL.String())
	if e != nil {
		writeError(w, 500, "token creation failed")
		return
	}
	writeJSON(w, 200, map[string]any{"access_token": a, "refresh_token": raw, "token_type": "Bearer", "expires_in": int(s.cfg.AccessTTL.Seconds())})
}
func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	var q struct {
		RefreshToken string `json:"refresh_token"`
	}
	if decode(r, &q) != nil || q.RefreshToken == "" {
		writeError(w, 400, "refresh_token required")
		return
	}
	h := auth.SHA256Hex([]byte(q.RefreshToken))
	var id, role string
	e := s.db.QueryRow(r.Context(), `SELECT u.id,u.role FROM refresh_tokens t JOIN users u ON u.id=t.user_id WHERE t.token_hash=$1 AND t.expires_at>now() AND t.revoked_at IS NULL`, h).Scan(&id, &role)
	if e != nil {
		writeError(w, 401, "invalid refresh token")
		return
	}
	_, _ = s.db.Exec(r.Context(), `UPDATE refresh_tokens SET revoked_at=now() WHERE token_hash=$1`, h)
	s.tokens(w, id, role)
}
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	id := userID(r)
	var out map[string]any
	var name, email, role string
	e := s.db.QueryRow(r.Context(), `SELECT name,email,role FROM users WHERE id=$1`, id).Scan(&name, &email, &role)
	if e != nil {
		writeError(w, 404, "user not found")
		return
	}
	out = map[string]any{"id": id, "name": name, "email": email, "role": role}
	writeJSON(w, 200, out)
}
func (s *Server) createEvent(w http.ResponseWriter, r *http.Request) {
	if role(r) != "organizer" && role(r) != "administrator" {
		writeError(w, 403, "organizer role required")
		return
	}
	var q struct {
		Title       string    `json:"title"`
		Description string    `json:"description"`
		Category    string    `json:"category"`
		VenueID     string    `json:"venue_id"`
		StartsAt    time.Time `json:"starts_at"`
		EndsAt      time.Time `json:"ends_at"`
		Currency    string    `json:"currency"`
		Capacity    int       `json:"capacity"`
	}
	if decode(r, &q) != nil || q.Title == "" || q.StartsAt.IsZero() || q.EndsAt.IsZero() || q.Capacity < 1 {
		writeError(w, 400, "invalid event fields")
		return
	}
	var id string
	e := s.db.QueryRow(r.Context(), `INSERT INTO events(organizer_id,title,description,category,venue_id,starts_at,ends_at,currency,capacity,status) VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,$6,$7,COALESCE(NULLIF($8,''),'INR'),$9,'draft') RETURNING id`, userID(r), q.Title, q.Description, q.Category, q.VenueID, q.StartsAt, q.EndsAt, q.Currency, q.Capacity).Scan(&id)
	if e != nil {
		writeError(w, 400, "could not create event: "+e.Error())
		return
	}
	writeJSON(w, 201, map[string]string{"id": id, "status": "draft"})
}
func (s *Server) publishEvent(w http.ResponseWriter, r *http.Request) {
	_, e := s.db.Exec(r.Context(), `UPDATE events SET status='published',updated_at=now() WHERE id=$1 AND (organizer_id=$2 OR $3='administrator') AND status='draft'`, r.PathValue("id"), userID(r), role(r))
	if e != nil {
		writeError(w, 500, "publish failed")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "published"})
}
func (s *Server) createTicketType(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		PriceMinor   int64  `json:"price_minor"`
		Currency     string `json:"currency"`
		Quantity     int    `json:"quantity"`
		PerUserLimit int    `json:"per_user_limit"`
	}
	if decode(r, &q) != nil || q.Name == "" || q.PriceMinor < 0 || q.Quantity < 1 {
		writeError(w, 400, "invalid ticket type")
		return
	}
	if q.Currency == "" {
		q.Currency = "INR"
	}
	if q.PerUserLimit < 1 || q.PerUserLimit > s.cfg.MaxTickets {
		q.PerUserLimit = s.cfg.MaxTickets
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "ticket inventory unavailable")
		return
	}
	defer tx.Rollback(r.Context())
	var capacity int
	err = tx.QueryRow(r.Context(), `SELECT capacity FROM events WHERE id=$1 AND (organizer_id=$2 OR $3='administrator') AND status='draft' FOR UPDATE`, r.PathValue("id"), userID(r), role(r)).Scan(&capacity)
	if err != nil {
		writeError(w, 404, "draft event not found or unauthorized")
		return
	}
	var allocated int
	err = tx.QueryRow(r.Context(), `SELECT COALESCE(SUM(quantity_total),0)::integer FROM ticket_types WHERE event_id=$1`, r.PathValue("id")).Scan(&allocated)
	if err != nil {
		writeError(w, 500, "ticket inventory unavailable")
		return
	}
	if allocated+q.Quantity > capacity {
		writeError(w, 409, "ticket inventory exceeds event capacity")
		return
	}
	var id string
	err = tx.QueryRow(r.Context(), `INSERT INTO ticket_types(event_id,name,description,price_minor,currency,quantity_total,per_user_limit) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, r.PathValue("id"), q.Name, q.Description, q.PriceMinor, q.Currency, q.Quantity, q.PerUserLimit).Scan(&id)
	if err != nil {
		writeError(w, 409, "ticket type could not be created")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "ticket inventory update failed")
		return
	}
	writeJSON(w, 201, map[string]string{"id": id})
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	rows, e := s.db.Query(r.Context(), `SELECT id,title,description,category,starts_at,ends_at,currency,status FROM events WHERE status='published' ORDER BY starts_at LIMIT 100`)
	if e != nil {
		writeError(w, 500, "could not load events")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, title, desc, cat, currency, status string
		var start, end time.Time
		if rows.Scan(&id, &title, &desc, &cat, &start, &end, &currency, &status) == nil {
			items = append(items, map[string]any{"id": id, "title": title, "description": desc, "category": cat, "starts_at": start, "ends_at": end, "currency": currency, "status": status})
		}
	}
	writeJSON(w, 200, items)
}

func (s *Server) ticketTypes(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT tt.id,tt.name,tt.description,tt.price_minor,tt.currency,
		tt.quantity_total-tt.quantity_sold-COALESCE((SELECT SUM(bi.quantity) FROM booking_items bi JOIN bookings b ON b.id=bi.booking_id
			WHERE bi.ticket_type_id=tt.id AND b.status='pending' AND (b.expires_at IS NULL OR b.expires_at>now())),0) AS available,tt.per_user_limit
		FROM ticket_types tt JOIN events e ON e.id=tt.event_id
		WHERE tt.event_id=$1 AND e.status='published' AND (tt.sales_start IS NULL OR tt.sales_start<=now()) AND (tt.sales_end IS NULL OR tt.sales_end>now())
		ORDER BY tt.price_minor,tt.name`, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "could not load ticket inventory")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, name, description, currency string
		var price int64
		var available, limit int
		if err = rows.Scan(&id, &name, &description, &price, &currency, &available, &limit); err != nil {
			writeError(w, 500, "could not load ticket inventory")
			return
		}
		items = append(items, map[string]any{"id": id, "name": name, "description": description, "price_minor": price, "currency": strings.TrimSpace(currency), "available": available, "per_user_limit": limit})
	}
	if err = rows.Err(); err != nil {
		writeError(w, 500, "could not load ticket inventory")
		return
	}
	writeJSON(w, 200, items)
}
func (s *Server) book(w http.ResponseWriter, r *http.Request) {
	var q struct {
		TicketTypeID   string `json:"ticket_type_id"`
		Quantity       int    `json:"quantity"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if decode(r, &q) != nil || q.TicketTypeID == "" || q.Quantity < 1 || q.Quantity > s.cfg.MaxTickets || q.IdempotencyKey == "" {
		writeError(w, 400, "ticket_type_id, quantity (within limit), and idempotency_key required")
		return
	}
	ctx := r.Context()
	tx, e := s.db.Begin(ctx)
	if e != nil {
		writeError(w, 500, "booking unavailable")
		return
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text,0))`, userID(r)); e != nil {
		writeError(w, 500, "booking unavailable")
		return
	}
	if e = expireReservations(ctx, tx); e != nil {
		writeError(w, 500, "booking inventory unavailable")
		return
	}
	var prior string
	e = tx.QueryRow(ctx, `SELECT response::text FROM idempotency_keys WHERE user_id=$1 AND key=$2 AND expires_at>now()`, userID(r), q.IdempotencyKey).Scan(&prior)
	if e == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		io.WriteString(w, prior)
		return
	}
	var eventID string
	var price int64
	var currency string
	var available, perUserLimit int
	e = tx.QueryRow(ctx, `SELECT tt.event_id,tt.price_minor,tt.currency,tt.quantity_total-tt.quantity_sold-tt.quantity_reserved,tt.per_user_limit FROM ticket_types tt JOIN events ev ON ev.id=tt.event_id WHERE tt.id=$1 AND ev.status='published' FOR UPDATE OF tt`, q.TicketTypeID).Scan(&eventID, &price, &currency, &available, &perUserLimit)
	if e != nil || available < q.Quantity {
		writeError(w, 409, "tickets unavailable")
		return
	}
	var alreadyBooked int
	e = tx.QueryRow(ctx, `SELECT COALESCE(SUM(bi.quantity),0)::integer FROM booking_items bi JOIN bookings b ON b.id=bi.booking_id
		WHERE b.user_id=$1 AND bi.ticket_type_id=$2 AND (b.status='paid' OR (b.status='pending' AND (b.expires_at IS NULL OR b.expires_at>now())))`, userID(r), q.TicketTypeID).Scan(&alreadyBooked)
	if e != nil {
		writeError(w, 500, "booking failed")
		return
	}
	if alreadyBooked+q.Quantity > perUserLimit {
		writeError(w, 409, "ticket purchase limit exceeded")
		return
	}
	var bid string
	var expiresAt time.Time
	e = tx.QueryRow(ctx, `INSERT INTO bookings(user_id,event_id,status,total_minor,currency,expires_at) VALUES($1,$2,'pending',$3,$4,now()+interval '15 minutes') RETURNING id,expires_at`, userID(r), eventID, price*int64(q.Quantity), currency).Scan(&bid, &expiresAt)
	if e != nil {
		writeError(w, 500, "booking failed")
		return
	}
	_, e = tx.Exec(ctx, `UPDATE ticket_types SET quantity_reserved=quantity_reserved+$2 WHERE id=$1`, q.TicketTypeID, q.Quantity)
	if e == nil {
		_, e = tx.Exec(ctx, `INSERT INTO booking_items(booking_id,ticket_type_id,quantity,unit_price_minor) VALUES($1,$2,$3,$4)`, bid, q.TicketTypeID, q.Quantity, price)
	}
	if e != nil {
		writeError(w, 500, "booking failed")
		return
	}
	body := map[string]any{"booking_id": bid, "status": "pending_payment", "amount_minor": price * int64(q.Quantity), "currency": currency, "expires_at": expiresAt, "payment_required": true}
	raw, _ := json.Marshal(body)
	_, e = tx.Exec(ctx, `INSERT INTO idempotency_keys(user_id,key,response,expires_at) VALUES($1,$2,$3::jsonb,now()+interval '24 hours')`, userID(r), q.IdempotencyKey, string(raw))
	if e == nil {
		e = tx.Commit(ctx)
	}
	if e != nil {
		writeError(w, 500, "booking failed")
		return
	}
	writeJSON(w, 201, body)
}

// Expired pending reservations are cancelled and inventory is released in one
// transaction. The next booking attempt runs this cleanup before checking stock.
func expireReservations(ctx context.Context, tx pgx.Tx) error {
	var released int64
	return tx.QueryRow(ctx, `WITH expired AS (
		UPDATE bookings SET status='cancelled',updated_at=now()
		WHERE status='pending' AND expires_at IS NOT NULL AND expires_at<=now()
		RETURNING id
	), quantities AS (
		SELECT bi.ticket_type_id,SUM(bi.quantity)::integer AS quantity
		FROM booking_items bi JOIN expired e ON e.id=bi.booking_id
		GROUP BY bi.ticket_type_id
	), released AS (
		UPDATE ticket_types tt SET quantity_reserved=GREATEST(0,tt.quantity_reserved-q.quantity)
		FROM quantities q WHERE tt.id=q.ticket_type_id RETURNING tt.id
	)
	SELECT count(*) FROM released`).Scan(&released)
}

func (s *Server) paymentOrder(w http.ResponseWriter, r *http.Request) {
	if s.cfg.RazorpayKeyID == "" || s.cfg.RazorpayKeySecret == "" {
		writeError(w, 503, "payments are not configured")
		return
	}
	var amount int64
	var currency string
	err := s.db.QueryRow(r.Context(), `SELECT total_minor,currency FROM bookings WHERE id=$1 AND user_id=$2 AND status='pending' AND (expires_at IS NULL OR expires_at>now())`, r.PathValue("id"), userID(r)).Scan(&amount, &currency)
	if err != nil {
		writeError(w, 404, "pending booking not found")
		return
	}
	var existing string
	err = s.db.QueryRow(r.Context(), `SELECT provider_order_id FROM payments WHERE booking_id=$1 AND status='created'`, r.PathValue("id")).Scan(&existing)
	if err == nil {
		writeJSON(w, 200, map[string]any{"order_id": existing, "amount_minor": amount, "currency": currency, "key_id": s.cfg.RazorpayKeyID})
		return
	}
	body, _ := json.Marshal(map[string]any{"amount": amount, "currency": currency, "receipt": r.PathValue("id"), "notes": map[string]string{"booking_id": r.PathValue("id")}})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "https://api.razorpay.com/v1/orders", strings.NewReader(string(body)))
	if err != nil {
		writeError(w, 500, "could not create payment order")
		return
	}
	req.SetBasicAuth(s.cfg.RazorpayKeyID, s.cfg.RazorpayKeySecret)
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		writeError(w, 502, "payment provider unavailable")
		return
	}
	defer resp.Body.Close()
	var result struct {
		ID    string          `json:"id"`
		Error json.RawMessage `json:"error"`
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result) != nil || result.ID == "" {
		writeError(w, 502, "payment order rejected")
		return
	}
	_, err = s.db.Exec(r.Context(), `INSERT INTO payments(booking_id,provider_order_id,status,amount_minor,currency) VALUES($1,$2,'created',$3,$4) ON CONFLICT(booking_id) DO UPDATE SET provider_order_id=EXCLUDED.provider_order_id,status='created',updated_at=now()`, r.PathValue("id"), result.ID, amount, currency)
	if err != nil {
		writeError(w, 500, "could not save payment order")
		return
	}
	writeJSON(w, 201, map[string]any{"order_id": result.ID, "amount_minor": amount, "currency": currency, "key_id": s.cfg.RazorpayKeyID})
}

func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	body, e := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if e != nil {
		writeError(w, 400, "invalid body")
		return
	}
	sig := r.Header.Get("X-Razorpay-Signature")
	if !verifyRazorpaySignature(body, sig, s.cfg.RazorpayWebhookSecret) {
		writeError(w, 401, "invalid webhook signature")
		return
	}
	var payload struct {
		Event   string `json:"event"`
		Payload struct {
			Payment struct {
				Entity struct {
					ID       string `json:"id"`
					OrderID  string `json:"order_id"`
					Status   string `json:"status"`
					Amount   int64  `json:"amount"`
					Currency string `json:"currency"`
				} `json:"entity"`
			} `json:"payment"`
		} `json:"payload"`
	}
	if json.Unmarshal(body, &payload) != nil {
		writeError(w, 400, "invalid webhook")
		return
	}
	if payload.Event != "payment.captured" && payload.Event != "payment.failed" {
		writeJSON(w, 200, map[string]string{"status": "ignored"})
		return
	}
	ctx := r.Context()
	tx, e := s.db.Begin(ctx)
	if e != nil {
		writeError(w, 500, "webhook processing failed")
		return
	}
	defer tx.Rollback(ctx)
	p := payload.Payload.Payment.Entity
	var bookingID, paymentStatus, paymentCurrency, bookingStatus, bookingCurrency string
	var paymentAmount, bookingAmount int64
	e = tx.QueryRow(ctx, `SELECT p.booking_id,p.status,p.amount_minor,p.currency,b.status,b.total_minor,b.currency
		FROM payments p JOIN bookings b ON b.id=p.booking_id
		WHERE p.provider_order_id=$1 FOR UPDATE OF p,b`, p.OrderID).Scan(&bookingID, &paymentStatus, &paymentAmount, &paymentCurrency, &bookingStatus, &bookingAmount, &bookingCurrency)
	if errors.Is(e, pgx.ErrNoRows) {
		writeError(w, 404, "payment order not found")
		return
	}
	if e != nil {
		writeError(w, 500, "webhook processing failed")
		return
	}
	if payload.Event == "payment.captured" && (p.Amount != paymentAmount || paymentAmount != bookingAmount || strings.TrimSpace(p.Currency) != strings.TrimSpace(paymentCurrency) || strings.TrimSpace(paymentCurrency) != strings.TrimSpace(bookingCurrency)) {
		writeError(w, 400, "captured payment amount or currency does not match booking")
		return
	}
	if paymentStatus == "captured" || paymentStatus == "refunded" {
		writeJSON(w, 200, map[string]string{"status": "already_processed"})
		return
	}
	if payload.Event == "payment.failed" {
		_, e = tx.Exec(ctx, `UPDATE payments SET status='failed',provider_payment_id=$2,updated_at=now() WHERE provider_order_id=$1`, p.OrderID, p.ID)
		if e == nil && bookingStatus == "pending" {
			var changed string
			e = tx.QueryRow(ctx, `UPDATE bookings SET status='failed',updated_at=now() WHERE id=$1 AND status='pending' RETURNING id`, bookingID).Scan(&changed)
			if errors.Is(e, pgx.ErrNoRows) {
				e = nil
			}
			if e == nil && changed != "" {
				e = releaseBookingReservation(ctx, tx, bookingID)
			}
		}
	} else {
		_, e = tx.Exec(ctx, `UPDATE payments SET status='captured',provider_payment_id=$2,updated_at=now() WHERE provider_order_id=$1`, p.OrderID, p.ID)
		if e == nil && bookingStatus == "pending" {
			var changed string
			e = tx.QueryRow(ctx, `UPDATE bookings SET status='paid',paid_at=now(),updated_at=now() WHERE id=$1 AND status='pending' AND (expires_at IS NULL OR expires_at>now()) RETURNING id`, bookingID).Scan(&changed)
			if errors.Is(e, pgx.ErrNoRows) {
				e = nil
			}
			if e == nil && changed != "" {
				_, e = tx.Exec(ctx, `UPDATE ticket_types tt SET quantity_reserved=GREATEST(0,tt.quantity_reserved-bi.quantity),quantity_sold=tt.quantity_sold+bi.quantity FROM booking_items bi WHERE bi.booking_id=$1 AND tt.id=bi.ticket_type_id`, bookingID)
				if e == nil {
					e = issueTickets(ctx, tx, bookingID)
				}
			} else if e == nil {
				// A late capture after its reservation expired must be surfaced for refund.
				if bookingStatus == "pending" {
					e = expireBooking(ctx, tx, bookingID)
				}
				if e == nil {
					e = queueRefundReview(ctx, tx, bookingID)
				}
			}
		} else if e == nil {
			e = queueRefundReview(ctx, tx, bookingID)
		}
	}
	if e != nil {
		writeError(w, 500, "webhook processing failed")
		return
	}
	if e = tx.Commit(ctx); e != nil {
		writeError(w, 500, "webhook processing failed")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "processed"})
}

func expireBooking(ctx context.Context, tx pgx.Tx, bookingID string) error {
	var expired string
	err := tx.QueryRow(ctx, `UPDATE bookings SET status='cancelled',updated_at=now()
		WHERE id=$1 AND status='pending' AND expires_at IS NOT NULL AND expires_at<=now() RETURNING id`, bookingID).Scan(&expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return releaseBookingReservation(ctx, tx, bookingID)
}

func releaseBookingReservation(ctx context.Context, tx pgx.Tx, bookingID string) error {
	_, err := tx.Exec(ctx, `UPDATE ticket_types tt
		SET quantity_reserved=GREATEST(0,tt.quantity_reserved-q.quantity)
		FROM (SELECT ticket_type_id,SUM(quantity)::integer AS quantity FROM booking_items WHERE booking_id=$1 GROUP BY ticket_type_id) q
		WHERE tt.id=q.ticket_type_id`, bookingID)
	return err
}

func queueRefundReview(ctx context.Context, tx pgx.Tx, bookingID string) error {
	var refundID, ownerID string
	err := tx.QueryRow(ctx, `INSERT INTO refunds(payment_id,amount_minor,status,reason)
		SELECT p.id,p.amount_minor,'requires_refund','payment captured after booking reservation was no longer active'
		FROM payments p WHERE p.booking_id=$1
	AND NOT EXISTS (SELECT 1 FROM refunds r WHERE r.payment_id=p.id AND r.status='requires_refund')
	RETURNING id`, bookingID).Scan(&refundID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT user_id FROM bookings WHERE id=$1`, bookingID).Scan(&ownerID); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"booking_id": bookingID, "refund_id": refundID, "reason": "late_payment_capture"})
	_, err = tx.Exec(ctx, `INSERT INTO notifications(user_id,channel,status,template,payload)
		VALUES($1,'email','queued','payment_refund_review',$2::jsonb)`, ownerID, string(payload))
	return err
}

// issueTickets creates high entropy QR credentials and stores only their hashes.
// The notification worker receives the one-time credential for secure delivery.
func issueTickets(ctx context.Context, tx pgx.Tx, bookingID string) error {
	var owner string
	if err := tx.QueryRow(ctx, `SELECT user_id FROM bookings WHERE id=$1`, bookingID).Scan(&owner); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT ticket_type_id,quantity FROM booking_items WHERE booking_id=$1`, bookingID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type item struct {
		typeID   string
		quantity int
	}
	items := []item{}
	for rows.Next() {
		var i item
		if err := rows.Scan(&i.typeID, &i.quantity); err != nil {
			return err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, i := range items {
		for n := 0; n < i.quantity; n++ {
			token, err := auth.RandomToken()
			if err != nil {
				return err
			}
			digest := auth.SHA256Hex([]byte(token))
			if _, err = tx.Exec(ctx, `INSERT INTO tickets(booking_id,ticket_type_id,owner_id,ticket_hash,qr_token_hash,status) VALUES($1,$2,$3,$4,$4,'active')`, bookingID, i.typeID, owner, digest); err != nil {
				return err
			}
			payload, _ := json.Marshal(map[string]string{"booking_id": bookingID, "qr_token": token})
			if _, err = tx.Exec(ctx, `INSERT INTO notifications(user_id,channel,status,template,payload) VALUES($1,'email','queued','ticket_issued',$2::jsonb)`, owner, string(payload)); err != nil {
				return err
			}
		}
	}
	return nil
}

type deviceContextKey struct{}

func (s *Server) verifyTicket(w http.ResponseWriter, r *http.Request) {
	var q struct {
		QRToken string `json:"qr_token"`
		ScanID  string `json:"scan_id"`
		EventID string `json:"event_id"`
	}
	if decode(r, &q) != nil || q.QRToken == "" || q.ScanID == "" {
		writeError(w, 400, "qr_token and scan_id required")
		return
	}
	id := r.Context().Value(deviceContextKey{}).(string)
	tx, e := s.db.Begin(r.Context())
	if e != nil {
		writeError(w, 500, "scan failed")
		return
	}
	defer tx.Rollback(r.Context())
	var ticketID, owner string
	e = tx.QueryRow(r.Context(), `SELECT t.id,t.owner_id FROM tickets t JOIN ticket_types tt ON tt.id=t.ticket_type_id WHERE t.qr_token_hash=encode(digest($1,'sha256'),'hex') AND t.status='active' AND (tt.event_id=NULLIF($2,'')::uuid OR $2='') FOR UPDATE OF t`, q.QRToken, q.EventID).Scan(&ticketID, &owner)
	valid := e == nil
	if valid {
		var exists bool
		_ = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM gate_scans WHERE device_id=$1 AND scan_id=$2)`, id, q.ScanID).Scan(&exists)
		if exists {
			valid = false
		} else {
			_, e = tx.Exec(r.Context(), `UPDATE tickets SET status='redeemed',redeemed_at=now(),redeemed_device_id=$2 WHERE id=$1 AND status='active'`, ticketID, id)
			valid = e == nil
		}
	}
	reason := "valid"
	if !valid {
		reason = "invalid_or_already_redeemed"
	}
	_, _ = tx.Exec(r.Context(), `INSERT INTO gate_scans(device_id,ticket_id,event_id,scan_id,result,reason,scanned_at) VALUES($1,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,$4,$5,$6,now()) ON CONFLICT(device_id,scan_id) DO NOTHING`, id, ticketID, q.EventID, q.ScanID, map[bool]string{true: "accepted", false: "rejected"}[valid], reason)
	_, _ = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_type,actor_id,action,entity_type,entity_id,metadata) VALUES('device',$1,'ticket_scan','ticket',NULLIF($2,'')::uuid,jsonb_build_object('result',$3,'scan_id',$4))`, id, ticketID, reason, q.ScanID)
	if e = tx.Commit(r.Context()); e != nil {
		writeError(w, 500, "scan failed")
		return
	}
	writeJSON(w, 200, map[string]any{"valid": valid, "gate": "open_if_valid", "reason": reason, "ticket_id": ticketID})
}
func (s *Server) readings(w http.ResponseWriter, r *http.Request) {
	var q struct {
		EventID    string         `json:"event_id"`
		IRState    string         `json:"ir_state"`
		DistanceCM float64        `json:"distance_cm"`
		ServoAngle int            `json:"servo_angle"`
		Payload    map[string]any `json:"payload"`
	}
	if decode(r, &q) != nil || q.DistanceCM < 0 || q.DistanceCM > 1000 || q.ServoAngle < 0 || q.ServoAngle > 180 {
		writeError(w, 400, "invalid sensor reading")
		return
	}
	id := r.Context().Value(deviceContextKey{}).(string)
	_, e := s.db.Exec(r.Context(), `INSERT INTO iot_readings(device_id,event_id,ir_state,distance_cm,servo_angle,payload,recorded_at) VALUES($1,NULLIF($2,'')::uuid,$3,$4,$5,$6,now())`, id, q.EventID, q.IRState, q.DistanceCM, q.ServoAngle, q.Payload)
	if e != nil {
		writeError(w, 500, "reading save failed")
		return
	}
	writeJSON(w, 202, map[string]string{"status": "accepted"})
}
func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.Context().Value(deviceContextKey{}).(string)
	_, e := s.db.Exec(r.Context(), `UPDATE gate_devices SET last_seen_at=now(),status='online' WHERE id=$1`, id)
	if e != nil {
		writeError(w, 500, "heartbeat failed")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "online"})
}
func (s *Server) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		c, e := auth.Parse(s.cfg.JWTSecret, h)
		if e != nil {
			writeError(w, 401, "unauthorized")
			return
		}
		ctx := context.WithValue(r.Context(), "user_id", c.Subject)
		ctx = context.WithValue(ctx, "role", c.Role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (s *Server) deviceAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.DeviceAPIKeyPepper == "" {
			writeError(w, 503, "gate device integration is not enabled")
			return
		}
		key := r.Header.Get("X-Device-Key")
		if key == "" {
			writeError(w, 401, "device key required")
			return
		}
		sum := auth.SHA256Hex([]byte(key + s.cfg.DeviceAPIKeyPepper))
		var id string
		e := s.db.QueryRow(r.Context(), `SELECT id FROM gate_devices WHERE api_key_hash=$1 AND status<>'revoked'`, sum).Scan(&id)
		if e != nil {
			writeError(w, 401, "invalid device credentials")
			return
		}
		ctx := context.WithValue(r.Context(), deviceContextKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (s *Server) limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		ip := strings.Split(r.RemoteAddr, ":")[0]
		key := ip + "/" + r.URL.Path
		rateMu.Lock()
		entry := rateWindows[key]
		now := time.Now()
		limit := int(s.cfg.RateLimitRPS * 60)
		if limit < 60 {
			limit = 600
		}
		if now.Sub(entry.start) >= time.Minute {
			entry = struct {
				start time.Time
				count int
			}{start: now}
		}
		entry.count++
		rateWindows[key] = entry
		blocked := entry.count > limit
		rateMu.Unlock()
		if blocked {
			w.Header().Set("Retry-After", "60")
			writeError(w, 429, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if x := recover(); x != nil {
				log.Printf("panic: %v", x)
				writeError(w, 500, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func decode(r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
func verifyRazorpaySignature(body []byte, signature, secret string) bool {
	if secret == "" || signature == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expected))
}
func userID(r *http.Request) string { v, _ := r.Context().Value("user_id").(string); return v }
func role(r *http.Request) string   { v, _ := r.Context().Value("role").(string); return v }

var _ = fmt.Sprintf
