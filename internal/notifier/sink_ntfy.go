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
	cfg      NtfySinkConfig
	client   *ntfy.Client
	periodic periodicState
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

func (s *NtfySink) NotifyPeriodic(ctx context.Context, records []ReportRecord) error {
	groups := groupPeriodicRecords(records, s.routeFor)
	for _, url := range sortedGroupKeys(groups) {
		signature := periodicDigest(groups[url])
		body := renderPeriodicPlain(groups[url])
		if s.periodic.unchanged(url, signature) {
			continue
		}
		if err := s.client.Post(ctx, url, body, ntfy.PostOptions{
			Title:    "Auth State",
			Priority: s.cfg.Priority,
			Tags:     s.cfg.Tags,
		}); err != nil {
			return err
		}
		s.periodic.remember(url, signature)
	}
	return nil
}

func (s *NtfySink) NotifySessionBatch(ctx context.Context, events []TruthRecord) error {
	groups := groupSessionEvents(events, s.routeFor)
	for _, url := range sortedTruthGroupKeys(groups) {
		v := buildSessionBatchView(groups[url])
		if err := s.client.Post(ctx, url, renderSessionBatchPlain(groups[url]), ntfy.PostOptions{
			Title:    v.Title,
			Priority: s.cfg.Priority,
			Tags:     s.cfg.Tags,
		}); err != nil {
			return err
		}
	}
	return nil
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
