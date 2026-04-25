package notifier

import (
	"context"

	"github.com/0xc0de1ab/pangaea/internal/notifier/ntfy"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// NtfyRoute pins a (profile, account) pair to one ntfy topic URL.
type NtfyRoute struct {
	Profile  string
	Account  string
	TopicURL string
}

type NtfySinkConfig struct {
	Routes          []NtfyRoute
	DefaultTopicURL string
	// AuthToken (optional) is the Bearer token for access-controlled
	// topics. Applied uniformly to all routes — operators with mixed
	// auth setups should run two notifier sink instances.
	AuthToken string
	// Priority is forwarded as the `Priority` header on every message.
	// 1 = min, 3 = default, 5 = max.
	Priority int
	// Tags is forwarded as the `Tags` header (comma-separated emoji
	// shortcodes / labels). Useful for distinguishing routine summaries
	// from expiry warnings in a single ntfy topic.
	Tags string
}

type NtfySink struct {
	cfg    NtfySinkConfig
	client *ntfy.Client
}

func NewNtfySink(cfg NtfySinkConfig, client *ntfy.Client) *NtfySink {
	if client == nil {
		client = ntfy.New()
	}
	if cfg.AuthToken != "" {
		client.AuthToken = cfg.AuthToken
	}
	return &NtfySink{cfg: cfg, client: client}
}

func (s *NtfySink) Name() string { return "ntfy" }

func (s *NtfySink) Notify(ctx context.Context, r TruthRecord, u formats.UsageReport) (bool, error) {
	url := s.routeFor(r.Profile, r.Account)
	if url == "" {
		return false, nil
	}
	title := buildView(r, u).Title
	body := renderNtfy(r, u)
	return true, s.client.Post(ctx, url, body, ntfy.PostOptions{
		Title:    title,
		Priority: s.cfg.Priority,
		Tags:     s.cfg.Tags,
	})
}

func (s *NtfySink) routeFor(profile, account string) string {
	for _, r := range s.cfg.Routes {
		if (r.Profile == "" || r.Profile == profile) &&
			(r.Account == "" || r.Account == account) {
			return r.TopicURL
		}
	}
	return s.cfg.DefaultTopicURL
}

// renderNtfy emits plain text — ntfy does not parse Markdown server-side
// (clients may render it but most don't). Each line is a labelled
// key/value to keep things scannable on a phone notification.
func renderNtfy(r TruthRecord, u formats.UsageReport) string {
	return renderPlainText(r, u)
}
