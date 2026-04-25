package notifier

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

type renderSummary struct {
	Identity         string            `json:"identity"`
	Subscription     string            `json:"subscription,omitempty"`
	FingerprintShort string            `json:"fingerprint_short,omitempty"`
	ExpiresAt        time.Time         `json:"expires_at,omitempty"`
	Extra            map[string]string `json:"extra,omitempty"`
}

type renderView struct {
	Heading        string
	Title          string
	Model          string
	Profile        string
	Account        string
	SourceNode     string
	TargetNodes    []string
	Format         string
	Fingerprint    string
	Plan           string
	Usage          string
	Validity       string
	ResetAt        string
	Notes          []string
	HasPropagation bool
	HasFingerprint bool
	HasValidity    bool
	HasUsage       bool
	HasReset       bool
	HasPlan        bool
	HasAccount     bool
	HasTargetNodes bool
	HasSourceNode  bool
}

type richMarkup struct {
	escape  func(string) string
	bold    func(string) string
	code    func(string) string
	lineEnd string
	bullet  string
	plain   bool
}

func buildView(r TruthRecord, u formats.UsageReport) renderView {
	sum := parseSummary(r.Summary)
	account := r.Account
	if account == "" {
		account = sum.Identity
	}
	if account == "" {
		account = "<no-account>"
	}

	targets := append([]string(nil), r.TargetNodes...)
	slices.Sort(targets)

	plan := strings.TrimSpace(u.PlanTier)
	if plan == "" {
		plan = strings.TrimSpace(sum.Subscription)
	}

	notes := make([]string, 0, len(u.Notes)+len(sum.Extra)+1)
	if plan != "" && sum.Subscription != "" && !strings.EqualFold(plan, sum.Subscription) {
		notes = append(notes, "subscription: "+sum.Subscription)
	}
	for _, n := range u.Notes {
		n = strings.TrimSpace(n)
		if n != "" {
			notes = append(notes, n)
		}
	}
	extraKeys := make([]string, 0, len(sum.Extra))
	for k := range sum.Extra {
		extraKeys = append(extraKeys, k)
	}
	slices.Sort(extraKeys)
	for _, k := range extraKeys {
		v := strings.TrimSpace(sum.Extra[k])
		if v == "" {
			continue
		}
		notes = append(notes, fmt.Sprintf("%s: %s", strings.ReplaceAll(k, "_", " "), v))
	}

	fingerprint := sum.FingerprintShort
	if fingerprint == "" {
		fingerprint = shortFP(r.Fingerprint)
	}

	usage := usageLine(u)
	validity := validityLine(sum.ExpiresAt)
	resetAt := timeLine(u.ResetAt)

	model := llmLabel(r.Format)
	heading := "Auth State"
	if r.SourceNode != "" && len(targets) > 0 {
		heading = "Auth Propagated"
	}

	title := fmt.Sprintf("%s · %s · %s", heading, model, shortAccount(account))

	return renderView{
		Heading:        heading,
		Title:          title,
		Model:          model,
		Profile:        r.Profile,
		Account:        account,
		SourceNode:     r.SourceNode,
		TargetNodes:    targets,
		Format:         r.Format,
		Fingerprint:    fingerprint,
		Plan:           plan,
		Usage:          usage,
		Validity:       validity,
		ResetAt:        resetAt,
		Notes:          notes,
		HasPropagation: r.SourceNode != "" && len(targets) > 0,
		HasFingerprint: fingerprint != "",
		HasValidity:    validity != "",
		HasUsage:       usage != "",
		HasReset:       resetAt != "",
		HasPlan:        plan != "",
		HasAccount:     account != "",
		HasTargetNodes: len(targets) > 0,
		HasSourceNode:  r.SourceNode != "",
	}
}

func renderRichText(r TruthRecord, u formats.UsageReport, m richMarkup) string {
	v := buildView(r, u)

	line := func(label, value string) string {
		if value == "" {
			return ""
		}
		if m.plain {
			return label + ": " + value
		}
		return m.bold(m.escape(label+":")) + " " + value
	}
	codeList := func(values []string) string {
		if len(values) == 0 {
			return ""
		}
		out := make([]string, 0, len(values))
		for _, value := range values {
			out = append(out, m.code(m.escape(value)))
		}
		return strings.Join(out, ", ")
	}

	var lines []string
	lines = append(lines, m.bold(m.escape(v.Heading)))
	lines = append(lines, fmt.Sprintf("%s · %s", m.bold(m.escape(v.Model)), m.code(m.escape(v.Account))))
	lines = append(lines, line("profile", m.escape(v.Profile)))
	lines = append(lines, line("llm", m.escape(v.Model)))
	if v.Format != "" && v.Format != v.Model {
		lines = append(lines, line("format", m.escape(v.Format)))
	}
	if v.HasSourceNode {
		lines = append(lines, line("source", m.code(m.escape(v.SourceNode))))
	}
	if v.HasTargetNodes {
		lines = append(lines, line(fmt.Sprintf("targets (%d)", len(v.TargetNodes)), codeList(v.TargetNodes)))
	}
	if v.HasPlan {
		lines = append(lines, line("plan", m.escape(v.Plan)))
	}
	if v.HasUsage {
		lines = append(lines, line("usage", m.escape(v.Usage)))
	}
	if v.HasValidity {
		lines = append(lines, line("valid until", m.escape(v.Validity)))
	}
	if v.HasReset {
		lines = append(lines, line("resets", m.escape(v.ResetAt)))
	}
	if v.HasFingerprint {
		lines = append(lines, line("fingerprint", m.code(m.escape(v.Fingerprint))))
	}
	for _, note := range v.Notes {
		if note == "" {
			continue
		}
		lines = append(lines, m.bullet+" "+m.escape(note))
	}
	return strings.Join(compact(lines), m.lineEnd)
}

func renderPlainText(r TruthRecord, u formats.UsageReport) string {
	return renderRichText(r, u, richMarkup{
		escape:  func(s string) string { return s },
		bold:    func(s string) string { return s },
		code:    func(s string) string { return s },
		lineEnd: "\n",
		bullet:  "*",
		plain:   true,
	})
}

func renderMarkdown(r TruthRecord, u formats.UsageReport) string {
	return renderRichText(r, u, richMarkup{
		escape:  mdEscape,
		bold:    func(s string) string { return "**" + s + "**" },
		code:    func(s string) string { return "`" + s + "`" },
		lineEnd: "\n",
		bullet:  "-",
	})
}

func renderMrkdwn(r TruthRecord, u formats.UsageReport) string {
	return renderRichText(r, u, richMarkup{
		escape:  mrkdwnEscape,
		bold:    func(s string) string { return "*" + s + "*" },
		code:    func(s string) string { return "`" + s + "`" },
		lineEnd: "\n",
		bullet:  "•",
	})
}

func renderHTML(r TruthRecord, u formats.UsageReport) string {
	return renderRichText(r, u, richMarkup{
		escape:  htmlEscape,
		bold:    func(s string) string { return "<b>" + s + "</b>" },
		code:    func(s string) string { return "<code>" + s + "</code>" },
		lineEnd: "\n",
		bullet:  "•",
	})
}

func renderTeamsText(r TruthRecord, u formats.UsageReport) string {
	return renderRichText(r, u, richMarkup{
		escape:  mdEscape,
		bold:    func(s string) string { return "**" + s + "**" },
		code:    func(s string) string { return "`" + s + "`" },
		lineEnd: "  \n",
		bullet:  "-",
	})
}

func parseSummary(raw json.RawMessage) renderSummary {
	if len(raw) == 0 {
		return renderSummary{}
	}
	var out renderSummary
	if err := json.Unmarshal(raw, &out); err != nil {
		return renderSummary{}
	}
	return out
}

func usageLine(u formats.UsageReport) string {
	if u.Limit > 0 {
		unit := strings.TrimSpace(u.Unit)
		if unit == "" {
			unit = "units"
		}
		remaining := u.Limit - u.Used
		if remaining < 0 {
			remaining = 0
		}
		remainingPct := 0.0
		if u.Limit > 0 {
			remainingPct = (float64(remaining) / float64(u.Limit)) * 100
		}
		return fmt.Sprintf("%d / %d %s used, %d left (%.1f%%)", u.Used, u.Limit, unit, remaining, remainingPct)
	}
	if u.RemainingPct > 0 {
		return fmt.Sprintf("%.1f%% left", u.RemainingPct)
	}
	return ""
}

func validityLine(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return timeLine(ts)
}

func timeLine(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return fmt.Sprintf("%s (%s)", ts.UTC().Format("2006-01-02 15:04 UTC"), relativeTime(ts))
}

func relativeTime(ts time.Time) string {
	d := time.Until(ts)
	if d < 0 {
		return humanDuration(-d) + " ago"
	}
	return "in " + humanDuration(d)
}

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return "less than 1m"
	}
	if d > 30*24*time.Hour {
		days := int(d.Round(24*time.Hour) / (24 * time.Hour))
		return fmt.Sprintf("%dd", days)
	}
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	hours := d / time.Hour
	d -= hours * time.Hour
	mins := d / time.Minute

	parts := make([]string, 0, 2)
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if mins > 0 && len(parts) < 2 {
		parts = append(parts, fmt.Sprintf("%dm", mins))
	}
	if len(parts) == 0 {
		return "less than 1m"
	}
	return strings.Join(parts, " ")
}

func llmLabel(format string) string {
	switch format {
	case "claude-credentials-json-format":
		return "Claude"
	case "codex-auth-json-format":
		return "Codex"
	case "gemini-oauth-creds-json-format":
		return "Gemini"
	default:
		return format
	}
}

func compact(lines []string) []string {
	out := lines[:0]
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}
