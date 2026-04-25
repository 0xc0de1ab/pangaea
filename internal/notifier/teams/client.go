// Package teams posts to Microsoft Teams Incoming Webhooks (legacy
// Office 365 Connector format). Body is a MessageCard envelope with
// Markdown text. The "Power Automate" / "Workflows" replacement pipeline
// uses Adaptive Cards but the classic webhook is still accepted for
// existing integrations.
package teams

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

// Card is the on-the-wire shape expected by Teams Incoming Webhooks.
type Card struct {
	Type       string `json:"@type"`
	Context    string `json:"@context"`
	Summary    string `json:"summary,omitempty"`
	ThemeColor string `json:"themeColor,omitempty"`
	Title      string `json:"title,omitempty"`
	Text       string `json:"text"`
}

// PostCard submits a MessageCard. Operators should pre-fill Summary +
// Title for readability; otherwise the card surfaces in the Teams chat
// as a plain text fragment.
func (c *Client) PostCard(ctx context.Context, webhookURL string, card Card) error {
	if webhookURL == "" {
		return fmt.Errorf("teams: webhook URL is empty")
	}
	if card.Type == "" {
		card.Type = "MessageCard"
	}
	if card.Context == "" {
		card.Context = "https://schema.org/extensions"
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	body, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("teams: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("teams: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("teams: send: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("teams: HTTP %d: %s", resp.StatusCode, scrubURL(string(respBody), webhookURL))
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
