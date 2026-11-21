package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Client defines the interface for delivering messages via Telegram Bot API.
type Client interface {
	SendMessage(ctx context.Context, chatID int64, text string) error
}

// HTTPClient implements Client by sending actual HTTPS requests to the Telegram Bot API.
type HTTPClient struct {
	token      string
	httpClient *http.Client
	apiBaseURL string
}

// NewHTTPClient creates a new Telegram HTTP client.
func NewHTTPClient(token string) *HTTPClient {
	return &HTTPClient{
		token: token,
		httpClient: &http.Client{
			Timeout: 8 * time.Second,
		},
		apiBaseURL: "https://api.telegram.org",
	}
}

// SetBaseURL overrides the API base URL (useful for mock servers in tests).
func (c *HTTPClient) SetBaseURL(url string) {
	c.apiBaseURL = url
}

// SendMessage sends an HTML-formatted message to the specified chat ID.
func (c *HTTPClient) SendMessage(ctx context.Context, chatID int64, text string) error {
	if c.token == "" {
		return errors.New("telegram bot token is not configured")
	}

	payload := map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal telegram payload: %w", err)
	}

	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", c.apiBaseURL, c.token)

	// Execute with single retry on 429 rate limit
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create telegram request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("dispatch telegram request: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			return nil
		}

		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			retryAfterSec := 1
			if h := resp.Header.Get("Retry-After"); h != "" {
				if s, e := strconv.Atoi(h); e == nil && s > 0 && s <= 5 {
					retryAfterSec = s
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(retryAfterSec) * time.Second):
				continue
			}
		}

		return fmt.Errorf("telegram API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return errors.New("telegram API call exceeded retry attempts")
}

// SentMessage records a message delivered to MockClient.
type SentMessage struct {
	ChatID int64
	Text   string
	SentAt time.Time
}

// MockClient implements Client for unit testing.
type MockClient struct {
	mu           sync.Mutex
	ShouldFail   bool
	FailError    error
	SentMessages []SentMessage
}

// NewMockClient creates a new MockClient.
func NewMockClient() *MockClient {
	return &MockClient{}
}

// SendMessage records or simulates failures for unit testing.
func (m *MockClient) SendMessage(ctx context.Context, chatID int64, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ShouldFail {
		if m.FailError != nil {
			return m.FailError
		}
		return errors.New("simulated telegram API network failure")
	}

	m.SentMessages = append(m.SentMessages, SentMessage{
		ChatID: chatID,
		Text:   text,
		SentAt: time.Now().UTC(),
	})
	return nil
}

// GetSentMessages returns a copy of recorded messages safely.
func (m *MockClient) GetSentMessages() []SentMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SentMessage, len(m.SentMessages))
	copy(out, m.SentMessages)
	return out
}
