// Package ntfy posts to ntfy.sh topics. Body is plain text (no JSON). Title
// and tags travel in headers. The topic URL itself is the routing target —
// e.g. https://ntfy.sh/my-account-1 — and is not a secret in the same
// sense as Slack/Discord webhooks (anyone who knows the topic can post),
// but we still treat it as configuration to keep secrets out of yaml.
package ntfy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	HTTP *http.Client
	// AuthToken is optional. ntfy supports access-controlled topics behind
	// auth.access_control reverse-proxies; the operator passes the bearer
	// token here.
	AuthToken string
}

func New() *Client { return &Client{HTTP: &http.Client{Timeout: 10 * time.Second}} }

// PostOptions tunes a single message.
type PostOptions struct {
	// Title becomes the notification title (Title header).
	Title string
	// Priority maps to ntfy 1-5 (5 = max). Zero falls back to default (3).
	Priority int
	// Tags is a comma-separated list of emoji shortcodes / tags.
	Tags string
}

// Post submits message body to topicURL. ntfy returns the published
// message JSON on 2xx; we ignore the body but surface non-2xx with the
// response preview.
func (c *Client) Post(ctx context.Context, topicURL, message string, opt PostOptions) error {
	if topicURL == "" {
		return fmt.Errorf("ntfy: topic URL is empty")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, topicURL, strings.NewReader(message))
	if err != nil {
		return fmt.Errorf("ntfy: new request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if opt.Title != "" {
		req.Header.Set("Title", opt.Title)
	}
	if opt.Priority >= 1 && opt.Priority <= 5 {
		req.Header.Set("Priority", fmt.Sprintf("%d", opt.Priority))
	}
	if opt.Tags != "" {
		req.Header.Set("Tags", opt.Tags)
	}
	if c.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy: send: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ntfy: HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
