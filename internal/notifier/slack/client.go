// Package slack is a minimal Slack Incoming Webhook poster. The webhook
// URL embeds the secret, so this client deliberately accepts the URL as a
// runtime input rather than building it from a token + path. We avoid
// pulling a third-party SDK for the same reason as the telegram client:
// the surface (POST a JSON body) is small enough to own.
package slack

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

// Client is stateless apart from the HTTP transport — webhook URLs vary
// per (account, channel), so they are passed to Post each time rather
// than baked in here.
type Client struct {
	HTTP *http.Client
}

// New returns a Client with a sensible default timeout.
func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// PostMessage submits a payload to a Slack incoming webhook. text is sent
// as the message body using Slack's `mrkdwn` syntax (the default for
// incoming webhooks). On non-200 responses we return an error that has
// any leaked URL fragments scrubbed.
func (c *Client) PostMessage(ctx context.Context, webhookURL, text string) error {
	if webhookURL == "" {
		return fmt.Errorf("slack: webhook URL is empty")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	body, err := json.Marshal(map[string]any{
		"text": text,
		// mrkdwn is the default; explicit for clarity.
		"mrkdwn": true,
	})
	if err != nil {
		return fmt.Errorf("slack: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("slack: send: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("slack: HTTP %d: %s", resp.StatusCode, scrubWebhook(string(respBody), webhookURL))
	}
	return nil
}

// scrubWebhook removes the webhook URL secret from any string before
// bubbling it out, so even an oddly-worded server response cannot echo
// the secret into operator logs.
func scrubWebhook(s, url string) string {
	if url == "" {
		return s
	}
	// The secret is everything after the last "/" in the URL path.
	if i := strings.LastIndex(url, "/"); i >= 0 && i < len(url)-1 {
		secret := url[i+1:]
		s = strings.ReplaceAll(s, secret, "<redacted>")
	}
	return strings.ReplaceAll(s, url, "<redacted>")
}
