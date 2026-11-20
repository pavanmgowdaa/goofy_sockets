package quai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	BaseURL, Token string
	HTTP           *http.Client
}
type State struct {
	Owner    string `json:"owner"`
	Redeemed bool   `json:"redeemed"`
	Exists   bool   `json:"exists"`
}
type Result struct {
	Status        string `json:"status"`
	TxHash        string `json:"tx_hash"`
	Confirmations int    `json:"confirmations"`
	BlockNumber   uint64 `json:"block_number"`
}

func (c Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}
func (c Client) call(ctx context.Context, method, path string, input, output any) error {
	if c.BaseURL == "" || c.Token == "" {
		return fmt.Errorf("Quai bridge is not configured")
	}
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = "Quai bridge rejected request"
		}
		return fmt.Errorf("Quai bridge HTTP %d: %s", resp.StatusCode, e.Error)
	}
	if output != nil {
		return json.Unmarshal(raw, output)
	}
	return nil
}
func (c Client) ValidateAddress(ctx context.Context, address string) (bool, error) {
	var out struct {
		Valid bool `json:"valid"`
	}
	err := c.call(ctx, http.MethodPost, "/v1/address/validate", map[string]string{"address": address}, &out)
	return out.Valid, err
}
func (c Client) Submit(ctx context.Context, operation, ticketID, target string) (Result, error) {
	var out Result
	err := c.call(ctx, http.MethodPost, "/v1/transactions", map[string]string{"operation": operation, "ticket_id": ticketID, "target_address": target}, &out)
	return out, err
}
func (c Client) Status(ctx context.Context, txHash string, confirmations int) (Result, error) {
	var out Result
	path := "/v1/transactions/" + url.PathEscape(txHash) + "?confirmations=" + fmt.Sprint(confirmations)
	err := c.call(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
func (c Client) Verify(ctx context.Context, ticketID string) (State, error) {
	var out State
	err := c.call(ctx, http.MethodGet, "/v1/tickets/"+url.PathEscape(ticketID), nil, &out)
	return out, err
}
