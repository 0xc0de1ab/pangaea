package notifier

import (
	"context"
	"strings"

	"github.com/0xc0de1ab/pangaea/internal/notifier/discord"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// DiscordRoute pins a (profile, account) pair to one Discord webhook URL.
type DiscordRoute struct {
	Profile    string
	Account    string
	WebhookURL string
}

type DiscordSinkConfig struct {
	Routes            []DiscordRoute
	DefaultWebhookURL string
}

type DiscordSink struct {
	cfg    DiscordSinkConfig
	client *discord.Client
}

func NewDiscordSink(cfg DiscordSinkConfig, client *discord.Client) *DiscordSink {
	if client == nil {
		client = discord.New()
	}
	return &DiscordSink{cfg: cfg, client: client}
}

func (s *DiscordSink) Name() string { return "discord" }

func (s *DiscordSink) Notify(ctx context.Context, r TruthRecord, u formats.UsageReport) (bool, error) {
	url := s.routeFor(r.Profile, r.Account)
	if url == "" {
		return false, nil
	}
	return true, s.client.Post(ctx, url, renderDiscord(r, u))
}

func (s *DiscordSink) routeFor(profile, account string) string {
	for _, r := range s.cfg.Routes {
		if (r.Profile == "" || r.Profile == profile) &&
			(r.Account == "" || r.Account == account) {
			return r.WebhookURL
		}
	}
	return s.cfg.DefaultWebhookURL
}

// renderDiscord uses Discord's Markdown: `**bold**`, single-backtick
// inline code, hyphen bullets. We deliberately keep the structure
// identical to the Slack/Telegram renderers so the same information
// shows up in the same order across all destinations.
func renderDiscord(r TruthRecord, u formats.UsageReport) string {
	return renderMarkdown(r, u)
}

// mdEscape escapes the small set of Markdown delimiters that would
// commonly mis-render in operator output: backticks, asterisks, and
// underscores. Backslash-escapes are honored by Discord, Mattermost, and
// Teams alike.
func mdEscape(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		"`", "\\`",
		"*", "\\*",
		"_", "\\_",
	)
	return r.Replace(s)
}
