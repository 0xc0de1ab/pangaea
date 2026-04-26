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
	cfg      DiscordSinkConfig
	client   *discord.Client
	periodic periodicState
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

func (s *DiscordSink) NotifyPeriodic(ctx context.Context, records []ReportRecord) error {
	groups := groupPeriodicRecords(records, s.routeFor)
	for _, url := range sortedGroupKeys(groups) {
		signature := periodicDigest(groups[url])
		body := renderPeriodicMarkdown(groups[url])
		if s.periodic.unchanged(url, signature) {
			continue
		}
		if err := s.client.Post(ctx, url, body); err != nil {
			return err
		}
		s.periodic.remember(url, signature)
	}
	return nil
}

func (s *DiscordSink) NotifySessionBatch(ctx context.Context, events []TruthRecord) error {
	groups := groupSessionEvents(events, s.routeFor)
	for _, url := range sortedTruthGroupKeys(groups) {
		if err := s.client.Post(ctx, url, renderSessionBatchMarkdown(groups[url])); err != nil {
			return err
		}
	}
	return nil
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
