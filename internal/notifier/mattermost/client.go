// Package mattermost posts to Mattermost Incoming Webhooks. The body is
// `{"text": "..."}` with Slack-compatible mrkdwn (single-asterisk bold,
// backtick code, dash bullets). Webhook URLs embed the channel + secret.
package mattermost

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

type Client struct{ HTTP *http.Client }

func New() *Client { return &Client{HTTP: &http.Client{Timeout: 10 * time.Second}} }

// Post submits a message. Mattermost returns 200 with `ok` on success and
// 400-class on malformed payloads. Webhook URL is scrubbed from any
// echoed error before bubbling out.
func (c *Client) Post(ctx context.Context, webhookURL, text string) error {
	if webhookURL == "" {
		return fmt.Errorf("mattermost: webhook URL is empty")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	body, err := json.Marshal(map[string]any{"text": text})
	if err != nil {
		return fmt.Errorf("mattermost: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mattermost: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("mattermost: send: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("mattermost: HTTP %d: %s", resp.StatusCode, scrubURL(string(respBody), webhookURL))
	}
	return nil
}

func scrubURL(s, url string) string {
	if url == "" {
		return s
	}
	if i := strings.LastIndex(url, "/"); i >= 0 && i < len(url)-1 {
		s = strings.ReplaceAll(s, url[i+1:], "<redacted>")
	}
	return strings.ReplaceAll(s, url, "<redacted>")
}
