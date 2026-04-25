// Package discord posts to Discord channel webhooks. The webhook URL
// embeds the channel + secret, so this client takes the URL per call and
// never persists it. Body is the standard `{ "content": "..." }` shape;
// Discord renders Markdown (** bold **, ` code `, > blockquote).
package discord

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

// Post submits a message to the given Discord webhook URL. Discord
// returns 204 on success and 200 with body on `?wait=true` queries; we
// accept both.
func (c *Client) Post(ctx context.Context, webhookURL, content string) error {
	if webhookURL == "" {
		return fmt.Errorf("discord: webhook URL is empty")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	body, err := json.Marshal(map[string]any{"content": content})
	if err != nil {
		return fmt.Errorf("discord: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("discord: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("discord: send: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("discord: HTTP %d: %s", resp.StatusCode, scrubURL(string(respBody), webhookURL))
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
