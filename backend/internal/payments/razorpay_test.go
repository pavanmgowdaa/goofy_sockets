package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateRefundUsesDocumentedIdempotencyHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/payments/pay_123/refund" {
			t.Fatalf("unexpected endpoint %s %s", r.Method, r.URL.Path)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "key" || pass != "secret" {
			t.Fatal("missing Razorpay basic auth")
		}
		if r.Header.Get("X-Refund-Idempotency") != "550e8400-e29b-41d4-a716-446655440000" {
			t.Fatal("missing refund idempotency key")
		}
		var body struct {
			Amount  int64  `json:"amount"`
			Receipt string `json:"receipt"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Amount != 1200 {
			t.Fatalf("unexpected request body: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"rfnd_123","payment_id":"pay_123","amount":1200,"status":"processed"}`))
	}))
	defer srv.Close()
	c := Razorpay{KeyID: "key", Secret: "secret", BaseURL: srv.URL + "/v1", HTTP: srv.Client()}
	refund, err := c.CreateRefund(context.Background(), "pay_123", 1200, "550e8400-e29b-41d4-a716-446655440000", "receipt-123")
	if err != nil {
		t.Fatal(err)
	}
	if refund.ID != "rfnd_123" || refund.Status != "processed" {
		t.Fatalf("unexpected response: %#v", refund)
	}
}
