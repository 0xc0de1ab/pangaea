package notifier

import (
	"context"

	"github.com/0xc0de1ab/pangaea/internal/notifier/teams"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// TeamsRoute pins a (profile, account) pair to one Teams webhook URL.
type TeamsRoute struct {
	Profile    string
	Account    string
	WebhookURL string
}

type TeamsSinkConfig struct {
	Routes            []TeamsRoute
	DefaultWebhookURL string
	// ThemeColor is the left-rail accent color (hex without `#`). Empty
	// falls back to Teams' default. Operators who want red for warnings
	// can route those through a dedicated webhook + sink.
	ThemeColor string
}

type TeamsSink struct {
	cfg    TeamsSinkConfig
	client *teams.Client
}

func NewTeamsSink(cfg TeamsSinkConfig, client *teams.Client) *TeamsSink {
	if client == nil {
		client = teams.New()
	}
	return &TeamsSink{cfg: cfg, client: client}
}

func (s *TeamsSink) Name() string { return "teams" }

func (s *TeamsSink) Notify(ctx context.Context, r TruthRecord, u formats.UsageReport) (bool, error) {
	url := s.routeFor(r.Profile, r.Account)
	if url == "" {
		return false, nil
	}
	title := buildView(r, u).Title
	card := teams.Card{
		Summary:    title,
		Title:      title,
		ThemeColor: s.cfg.ThemeColor,
		Text:       renderTeams(r, u),
	}
	return true, s.client.PostCard(ctx, url, card)
}

func (s *TeamsSink) routeFor(profile, account string) string {
	for _, r := range s.cfg.Routes {
		if (r.Profile == "" || r.Profile == profile) &&
			(r.Account == "" || r.Account == account) {
			return r.WebhookURL
		}
	}
	return s.cfg.DefaultWebhookURL
}

// renderTeams uses Teams' Markdown subset (the MessageCard `text` field
// supports a fairly conservative Markdown — bold via **, line breaks need
// double newlines for paragraph separation, lists with `- `).
func renderTeams(r TruthRecord, u formats.UsageReport) string {
	return renderTeamsText(r, u)
}
