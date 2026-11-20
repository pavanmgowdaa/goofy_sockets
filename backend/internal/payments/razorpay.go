package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Razorpay struct {
	KeyID, Secret, BaseURL string
	HTTP                   *http.Client
}
type Order struct {
	ID       string `json:"id"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Status   string `json:"status"`
}
type Refund struct {
	ID        string `json:"id"`
	PaymentID string `json:"payment_id"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Status    string `json:"status"`
}
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Razorpay returned HTTP %d: %s", e.StatusCode, e.Message)
}

func (c Razorpay) endpoint(path string) string {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = "https://api.razorpay.com/v1"
	}
	return base + path
}
func (c Razorpay) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 12 * time.Second}
}
func (c Razorpay) request(ctx context.Context, method, path string, body any, headers map[string]string, target any) error {
	if c.KeyID == "" || c.Secret == "" {
		return fmt.Errorf("Razorpay credentials are not configured")
	}
	var payload io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), payload)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.KeyID, c.Secret)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			Error struct {
				Description string `json:"description"`
				Reason      string `json:"reason"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &envelope)
		msg := envelope.Error.Description
		if msg == "" {
			msg = envelope.Error.Reason
		}
		if msg == "" {
			msg = "provider request failed"
		}
		return &APIError{StatusCode: resp.StatusCode, Message: msg}
	}
	if target == nil {
		return nil
	}
	if err = json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode Razorpay response: %w", err)
	}
	return nil
}
func (c Razorpay) CreateOrder(ctx context.Context, amount int64, currency, receipt string, notes map[string]string) (Order, error) {
	var result Order
	err := c.request(ctx, http.MethodPost, "/orders", map[string]any{"amount": amount, "currency": currency, "receipt": receipt, "notes": notes}, nil, &result)
	if err == nil && result.ID == "" {
		err = fmt.Errorf("Razorpay order response had no id")
	}
	return result, err
}
func (c Razorpay) CreateRefund(ctx context.Context, paymentID string, amount int64, refundKey, receipt string) (Refund, error) {
	var result Refund
	err := c.request(ctx, http.MethodPost, "/payments/"+paymentID+"/refund", map[string]any{"amount": amount, "receipt": receipt, "notes": map[string]string{"source": "fairpass"}}, map[string]string{"X-Refund-Idempotency": refundKey}, &result)
	if err == nil && result.ID == "" {
		err = fmt.Errorf("Razorpay refund response had no id")
	}
	return result, err
}
