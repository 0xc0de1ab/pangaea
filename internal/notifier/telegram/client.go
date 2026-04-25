// Package telegram is a minimal Telegram Bot API client tailored to the
// notifier's needs: send a single text message to a chat. We deliberately
// avoid pulling a third-party SDK — the surface we use (POST sendMessage)
// is small enough that the maintenance cost of a 60-line client is lower
// than tracking an upstream library.
package telegram

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

// DefaultEndpoint is the public Bot API host. Tests inject a different one
// via Client.Endpoint.
const DefaultEndpoint = "https://api.telegram.org"

// Client is a thin Bot API wrapper. Construct with New; the zero value is
// not valid (BotToken is required).
type Client struct {
	// BotToken is the secret the BotFather hands out. It is the only
	// authenticator; never log or echo it.
	BotToken string
	// Endpoint defaults to DefaultEndpoint. Override for testing.
	Endpoint string
	// HTTP defaults to a 10s-timeout client. Override to inject a transport
	// with proxy support or instrumentation.
	HTTP *http.Client
}

// New returns a Client with sensible defaults.
func New(botToken string) *Client {
	return &Client{
		BotToken: botToken,
		Endpoint: DefaultEndpoint,
		HTTP:     &http.Client{Timeout: 10 * time.Second},
	}
}

// SendMessageRequest mirrors the bot API parameters we use. ParseMode is
// optional (we use "HTML" for safe formatting); DisableNotification keeps
// routine usage updates from buzzing the chat at 3am.
type SendMessageRequest struct {
	ChatID              string `json:"chat_id"`
	Text                string `json:"text"`
	ParseMode           string `json:"parse_mode,omitempty"`
	DisableNotification bool   `json:"disable_notification,omitempty"`
}

// SendMessage POSTs sendMessage. On any non-200 / Telegram-error the
// returned error includes the response description so operators can see
// why; the body never includes the bot token because we send it in the URL
// not the body.
func (c *Client) SendMessage(ctx context.Context, req SendMessageRequest) error {
	if c.BotToken == "" {
		return fmt.Errorf("telegram: BotToken is empty")
	}
	if req.ChatID == "" {
		return fmt.Errorf("telegram: ChatID is empty")
	}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("telegram: marshal: %w", err)
	}
	url := strings.TrimRight(endpoint, "/") + "/bot" + c.BotToken + "/sendMessage"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(httpReq)
	if err != nil {
		return fmt.Errorf("telegram: send: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("telegram: HTTP %d: %s", resp.StatusCode, scrubToken(string(respBody), c.BotToken))
	}
	var apiResp struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err == nil && !apiResp.OK {
		return fmt.Errorf("telegram: api: %s", apiResp.Description)
	}
	return nil
}

// scrubToken removes the bot token from any error string before bubbling it
// out — Telegram echoes the URL in some error messages.
func scrubToken(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "<redacted>")
}
