package notifier

import (
	"context"
	"strings"

	"github.com/0xc0de1ab/pangaea/internal/notifier/slack"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// SlackRoute pins one (profile, account) tuple to one Slack incoming
// webhook URL. Empty Profile/Account fields act as wildcards. Webhook
// URLs embed the channel + secret, so per-account routing is a matter
// of registering one webhook per target channel and listing them here.
type SlackRoute struct {
	Profile    string
	Account    string
	WebhookURL string
}

// SlackSinkConfig configures the Slack fan-out target.
type SlackSinkConfig struct {
	Routes            []SlackRoute
	DefaultWebhookURL string
}

// SlackSink is a Sink that posts to Slack incoming webhooks. Each webhook
// URL is treated as a secret and never logged.
type SlackSink struct {
	cfg    SlackSinkConfig
	client *slack.Client
}

// NewSlackSink constructs a sink. client may be nil to use a freshly-built
// default with a 10s timeout.
func NewSlackSink(cfg SlackSinkConfig, client *slack.Client) *SlackSink {
	if client == nil {
		client = slack.New()
	}
	return &SlackSink{cfg: cfg, client: client}
}

// Name returns the sink label used in logs.
func (s *SlackSink) Name() string { return "slack" }

// Notify resolves a webhook URL for (profile, account) and posts a mrkdwn
// message. Returns (false, nil) when no route matches and no default URL
// is set.
func (s *SlackSink) Notify(ctx context.Context, r TruthRecord, u formats.UsageReport) (bool, error) {
	url := s.routeFor(r.Profile, r.Account)
	if url == "" {
		return false, nil
	}
	return true, s.client.PostMessage(ctx, url, renderSlack(r, u))
}

func (s *SlackSink) routeFor(profile, account string) string {
	for _, r := range s.cfg.Routes {
		if (r.Profile == "" || r.Profile == profile) &&
			(r.Account == "" || r.Account == account) {
			return r.WebhookURL
		}
	}
	return s.cfg.DefaultWebhookURL
}

// renderSlack composes the human-facing text in Slack's mrkdwn syntax.
// Slack's mrkdwn is similar to but not the same as Markdown — `*bold*`
// (one asterisk), “ `code` “, plain newlines. We deliberately keep the
// structure identical to the Telegram renderer so the two messages read
// the same, only the markup differs.
func renderSlack(r TruthRecord, u formats.UsageReport) string {
	return renderMrkdwn(r, u)
}

// mrkdwnEscape escapes the three characters Slack's mrkdwn treats
// specially as message delimiters. Other characters (* _ ` ~) are also
// markup but operators may want them rendered literally; per Slack's
// recommendation, only & < > are required to be escaped.
func mrkdwnEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
