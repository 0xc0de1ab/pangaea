package notifier

import (
	"context"

	"github.com/0xc0de1ab/pangaea/internal/notifier/mattermost"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// MattermostRoute pins a (profile, account) pair to one webhook URL.
type MattermostRoute struct {
	Profile    string
	Account    string
	WebhookURL string
}

type MattermostSinkConfig struct {
	Routes            []MattermostRoute
	DefaultWebhookURL string
}

type MattermostSink struct {
	cfg    MattermostSinkConfig
	client *mattermost.Client
}

func NewMattermostSink(cfg MattermostSinkConfig, client *mattermost.Client) *MattermostSink {
	if client == nil {
		client = mattermost.New()
	}
	return &MattermostSink{cfg: cfg, client: client}
}

func (s *MattermostSink) Name() string { return "mattermost" }

func (s *MattermostSink) Notify(ctx context.Context, r TruthRecord, u formats.UsageReport) (bool, error) {
	url := s.routeFor(r.Profile, r.Account)
	if url == "" {
		return false, nil
	}
	return true, s.client.Post(ctx, url, renderMattermost(r, u))
}

func (s *MattermostSink) routeFor(profile, account string) string {
	for _, r := range s.cfg.Routes {
		if (r.Profile == "" || r.Profile == profile) &&
			(r.Account == "" || r.Account == account) {
			return r.WebhookURL
		}
	}
	return s.cfg.DefaultWebhookURL
}

// renderMattermost uses Mattermost's mrkdwn (Slack-compatible single-
// asterisk bold, backtick code, hyphen bullets). The renderer is a clone
// of the Slack one; we intentionally do not share the function so a
// future Mattermost-specific divergence (e.g. emoji shortcodes) lands
// in one place.
func renderMattermost(r TruthRecord, u formats.UsageReport) string {
	return renderMrkdwn(r, u)
}
