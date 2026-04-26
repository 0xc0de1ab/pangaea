package notifier

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

var nowFunc = time.Now

type renderSummary struct {
	Identity         string            `json:"identity"`
	Subscription     string            `json:"subscription,omitempty"`
	FingerprintShort string            `json:"fingerprint_short,omitempty"`
	ExpiresAt        time.Time         `json:"expires_at,omitempty"`
	Extra            map[string]string `json:"extra,omitempty"`
}

type renderView struct {
	Heading         string
	Title           string
	EventKind       string
	NodeID          string
	PeerCN          string
	AuthMode        string
	Model           string
	Profile         string
	Account         string
	AccountID       string
	Nodes           []string
	LastRefresh     string
	SourceNode      string
	TargetNodes     []string
	Format          string
	Fingerprint     string
	Plan            string
	Usage           string
	UsageDetails    []string
	Validity        string
	ExpiryChange    string
	ResetAt         string
	Notes           []string
	HasPropagation  bool
	HasFingerprint  bool
	HasValidity     bool
	HasExpiryChange bool
	HasUsage        bool
	HasReset        bool
	HasPlan         bool
	HasAccount      bool
	HasLastRefresh  bool
	HasTargetNodes  bool
	HasSourceNode   bool
	HasNodes        bool
	IsSessionEvent  bool
}

type sessionBatchView struct {
	Heading  string
	Title    string
	NodeID   string
	PeerCN   string
	AuthMode string
	Rows     []sessionBatchRow
}

type sessionBatchRow struct {
	Profile   string
	Model     string
	Connected string
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
	if r.EventKind == EventSessionConnected || r.EventKind == EventSessionDisconnected || r.EventKind == EventSessionReconnected {
		heading := "Node Connected"
		if r.EventKind == EventSessionDisconnected {
			heading = "Node Disconnected"
		} else if r.EventKind == EventSessionReconnected {
			heading = "Node Reconnected"
		}
		model := llmLabel(r.Format)
		nodes := normalizedNodes(r)
		return renderView{
			Heading:        heading,
			Title:          fmt.Sprintf("%s · %s · %s", heading, model, r.NodeID),
			EventKind:      r.EventKind,
			NodeID:         r.NodeID,
			PeerCN:         r.PeerCN,
			AuthMode:       r.AuthMode,
			Model:          model,
			Profile:        r.Profile,
			Nodes:          nodes,
			HasNodes:       len(nodes) > 0,
			IsSessionEvent: true,
		}
	}

	sum := parseSummary(r.Summary)
	if r.EventKind == EventTruthLost && len(sum.Extra) == 0 && sum.Identity == "" && sum.ExpiresAt.IsZero() {
		sum = parseSummary(r.PrevSummary)
	}
	account := r.Account
	if account == "" {
		account = sum.Identity
	}
	if account == "" {
		account = "<no-account>"
	}
	accountID := strings.TrimSpace(sum.Extra["account_id"])
	if accountID == "" {
		accountID = account
	}
	accountID = displayAccountID(accountID)

	lastRefresh := ""
	if ts, ok := parseSummaryTime(sum.Extra["last_refresh"]); ok {
		lastRefresh = seoulTimeLine(ts, false)
	}

	targets := append([]string(nil), r.TargetNodes...)
	slices.Sort(targets)
	nodes := normalizedNodes(r)

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
		switch k {
		case "account_id", "email", "masked_email", "last_refresh":
			continue
		}
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
	usageDetails := usageDetailLines(u)
	validity := validityLine(sum.ExpiresAt)
	expiryChange := expiryChangeLine(parseSummary(r.PrevSummary), sum)
	resetAt := timeLine(u.ResetAt)

	model := llmLabel(r.Format)
	heading := "Auth State"
	if r.EventKind == EventTruthRestored {
		heading = "Truth Restored"
	} else if r.EventKind == EventTruthLost {
		heading = "Truth Lost"
	} else if r.SourceNode != "" && len(targets) > 0 {
		heading = "Auth Propagated"
	}
	title := fmt.Sprintf("%s · %s · %s", heading, model, shortAccount(accountID))

	return renderView{
		Heading:         heading,
		Title:           title,
		Model:           model,
		Profile:         r.Profile,
		Account:         account,
		AccountID:       accountID,
		Nodes:           nodes,
		LastRefresh:     lastRefresh,
		SourceNode:      r.SourceNode,
		TargetNodes:     targets,
		Format:          r.Format,
		Fingerprint:     fingerprint,
		Plan:            plan,
		Usage:           usage,
		UsageDetails:    usageDetails,
		Validity:        validity,
		ExpiryChange:    expiryChange,
		ResetAt:         resetAt,
		Notes:           notes,
		HasPropagation:  r.SourceNode != "" && len(targets) > 0,
		HasFingerprint:  fingerprint != "",
		HasValidity:     validity != "",
		HasExpiryChange: expiryChange != "",
		HasUsage:        usage != "",
		HasReset:        resetAt != "",
		HasPlan:         plan != "",
		HasAccount:      accountID != "",
		HasLastRefresh:  lastRefresh != "",
		HasTargetNodes:  len(targets) > 0,
		HasSourceNode:   r.SourceNode != "",
		HasNodes:        len(nodes) > 0,
	}
}

func buildSessionBatchView(events []TruthRecord) sessionBatchView {
	if len(events) == 0 {
		return sessionBatchView{}
	}
	first := events[0]
	heading := "Node Connected"
	if first.EventKind == EventSessionDisconnected {
		heading = "Node Disconnected"
	} else if first.EventKind == EventSessionReconnected {
		heading = "Node Reconnected"
	}
	rows := make([]sessionBatchRow, 0, len(events))
	for _, event := range events {
		rows = append(rows, sessionBatchRow{
			Profile:   event.Profile,
			Model:     llmLabel(event.Format),
			Connected: strings.Join(normalizedNodes(event), ", "),
		})
	}
	slices.SortFunc(rows, func(a, b sessionBatchRow) int {
		if a.Profile < b.Profile {
			return -1
		}
		if a.Profile > b.Profile {
			return 1
		}
		return 0
	})
	return sessionBatchView{
		Heading:  heading,
		Title:    fmt.Sprintf("%s · %s", heading, first.NodeID),
		NodeID:   first.NodeID,
		PeerCN:   first.PeerCN,
		AuthMode: first.AuthMode,
		Rows:     rows,
	}
}

func renderRichText(r TruthRecord, u formats.UsageReport, m richMarkup) string {
	v := buildView(r, u)
	if v.IsSessionEvent {
		lines := []string{
			m.bold(m.escape(v.Heading)),
			lineWithMarkup(m, "profile", m.escape(v.Profile)),
			lineWithMarkup(m, "llm", m.escape(v.Model)),
			lineWithMarkup(m, "node", m.code(m.escape(v.NodeID))),
		}
		if v.AuthMode != "" {
			lines = append(lines, lineWithMarkup(m, "auth mode", m.escape(v.AuthMode)))
		}
		if v.PeerCN != "" {
			lines = append(lines, lineWithMarkup(m, "peer cn", m.code(m.escape(v.PeerCN))))
		}
		if v.HasNodes {
			lines = append(lines, lineWithMarkup(m, fmt.Sprintf("connected nodes (%d)", len(v.Nodes)), codeListWithMarkup(m, v.Nodes)))
		}
		return strings.Join(compact(lines), m.lineEnd)
	}

	line := func(label, value string) string {
		return lineWithMarkup(m, label, value)
	}
	codeList := func(values []string) string {
		return codeListWithMarkup(m, values)
	}

	var lines []string
	lines = append(lines, m.bold(m.escape(v.Heading)))
	lines = append(lines, line("profile", m.escape(v.Profile)))
	lines = append(lines, line("llm", m.escape(v.Model)))
	if v.Format != "" && v.Format != v.Model {
		lines = append(lines, line("format", m.escape(v.Format)))
	}
	if v.HasNodes {
		lines = append(lines, line(fmt.Sprintf("nodes (%d)", len(v.Nodes)), codeList(v.Nodes)))
	}
	if v.HasSourceNode {
		lines = append(lines, line("latest detected", m.code(m.escape(v.SourceNode))))
	}
	if v.HasTargetNodes {
		lines = append(lines, line(fmt.Sprintf("propagated to (%d)", len(v.TargetNodes)), codeList(v.TargetNodes)))
	}
	if v.HasAccount {
		lines = append(lines, line("account id", m.code(m.escape(v.AccountID))))
	}
	if v.HasLastRefresh {
		lines = append(lines, line("last refresh", m.escape(v.LastRefresh)))
	}
	if v.HasPlan {
		lines = append(lines, line("plan", m.escape(v.Plan)))
	}
	if v.HasUsage {
		lines = append(lines, line("usage", m.escape(v.Usage)))
	}
	for _, detail := range v.UsageDetails {
		if detail == "" {
			continue
		}
		lines = append(lines, m.bullet+" "+m.escape(detail))
	}
	if v.HasExpiryChange {
		lines = append(lines, line("expiry change", m.escape(v.ExpiryChange)))
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

func lineWithMarkup(m richMarkup, label, value string) string {
	if value == "" {
		return ""
	}
	if m.plain {
		return label + ": " + value
	}
	return m.bold(m.escape(label+":")) + " " + value
}

func codeListWithMarkup(m richMarkup, values []string) string {
	if len(values) == 0 {
		return ""
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, m.code(m.escape(value)))
	}
	return strings.Join(out, ", ")
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

func renderSessionBatchPlain(events []TruthRecord) string {
	v := buildSessionBatchView(events)
	lines := sessionBatchHeaderLines(v)
	lines = append(lines, "")
	lines = append(lines, sessionBatchTableLines(v)...)
	return strings.Join(compact(lines), "\n")
}

func renderSessionBatchMarkdown(events []TruthRecord) string {
	v := buildSessionBatchView(events)
	lines := []string{"**" + mdEscape(v.Heading) + "**"}
	lines = append(lines, sessionBatchHeaderMarkupLines(v, richMarkup{
		escape:  mdEscape,
		bold:    func(s string) string { return "**" + s + "**" },
		code:    func(s string) string { return "`" + s + "`" },
		lineEnd: "\n",
		bullet:  "-",
	})...)
	lines = append(lines, "")
	lines = append(lines, "```")
	lines = append(lines, sessionBatchTableLines(v)...)
	lines = append(lines, "```")
	return strings.Join(compact(lines), "\n")
}

func renderSessionBatchMrkdwn(events []TruthRecord) string {
	v := buildSessionBatchView(events)
	lines := []string{"*" + mrkdwnEscape(v.Heading) + "*"}
	lines = append(lines, sessionBatchHeaderMarkupLines(v, richMarkup{
		escape:  mrkdwnEscape,
		bold:    func(s string) string { return "*" + s + "*" },
		code:    func(s string) string { return "`" + s + "`" },
		lineEnd: "\n",
		bullet:  "•",
	})...)
	lines = append(lines, "")
	lines = append(lines, "```")
	lines = append(lines, sessionBatchTableLines(v)...)
	lines = append(lines, "```")
	return strings.Join(compact(lines), "\n")
}

func renderSessionBatchTeams(events []TruthRecord) string {
	v := buildSessionBatchView(events)
	lines := []string{"**" + mdEscape(v.Heading) + "**"}
	lines = append(lines, sessionBatchHeaderMarkupLines(v, richMarkup{
		escape:  mdEscape,
		bold:    func(s string) string { return "**" + s + "**" },
		code:    func(s string) string { return "`" + s + "`" },
		lineEnd: "  \n",
		bullet:  "-",
	})...)
	lines = append(lines, "")
	lines = append(lines, sessionBatchTableLines(v)...)
	return strings.Join(compact(lines), "  \n")
}

func sessionBatchHeaderLines(v sessionBatchView) []string {
	lines := []string{
		"Node: " + v.NodeID,
	}
	if v.AuthMode != "" {
		lines = append(lines, "Auth Mode: "+v.AuthMode)
	}
	if v.PeerCN != "" {
		lines = append(lines, "Peer CN: "+v.PeerCN)
	}
	return lines
}

func sessionBatchHeaderMarkupLines(v sessionBatchView, m richMarkup) []string {
	lines := []string{
		lineWithMarkup(m, "node", m.code(m.escape(v.NodeID))),
	}
	if v.AuthMode != "" {
		lines = append(lines, lineWithMarkup(m, "auth mode", m.escape(v.AuthMode)))
	}
	if v.PeerCN != "" {
		lines = append(lines, lineWithMarkup(m, "peer cn", m.code(m.escape(v.PeerCN))))
	}
	return lines
}

func sessionBatchTableLines(v sessionBatchView) []string {
	lines := []string{
		fmt.Sprintf("%-10s %-8s %s", "PROFILE", "LLM", "CONNECTED"),
	}
	for _, row := range v.Rows {
		lines = append(lines, fmt.Sprintf(
			"%-10s %-8s %s",
			row.Profile,
			row.Model,
			row.Connected,
		))
	}
	return lines
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
	if len(u.Windows) > 0 {
		return ""
	}
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

func usageDetailLines(u formats.UsageReport) []string {
	if len(u.Windows) == 0 {
		return nil
	}
	out := make([]string, 0, len(u.Windows))
	for _, w := range u.Windows {
		label := strings.TrimSpace(w.Label)
		if label == "" {
			label = "usage"
		}
		line := label + ": " + usageWindowLine(w)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func usageWindowLine(w formats.UsageWindow) string {
	parts := make([]string, 0, 2)
	switch {
	case w.Limit > 0:
		remaining := w.Limit - w.Used
		if remaining < 0 {
			remaining = 0
		}
		remainingPct := w.RemainingPct
		if remainingPct <= 0 && w.Limit > 0 {
			remainingPct = (float64(remaining) / float64(w.Limit)) * 100
		}
		parts = append(parts, fmt.Sprintf("%.0f%% left", remainingPct))
	case w.RemainingPct > 0:
		parts = append(parts, fmt.Sprintf("%.0f%% left", w.RemainingPct))
	default:
		if !w.ResetAt.IsZero() {
			return timeLine(w.ResetAt)
		}
		return ""
	}
	if !w.ResetAt.IsZero() {
		parts = append(parts, "resets "+timeLine(w.ResetAt))
	}
	return strings.Join(parts, ", ")
}

func validityLine(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return timeLine(ts)
}

func expiryChangeLine(prev, cur renderSummary) string {
	if prev.ExpiresAt.IsZero() || cur.ExpiresAt.IsZero() || prev.ExpiresAt.Equal(cur.ExpiresAt) {
		return ""
	}
	return fmt.Sprintf("%s -> %s", timeLine(prev.ExpiresAt), timeLine(cur.ExpiresAt))
}

func timeLine(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return fmt.Sprintf("%s (%s)", ts.In(seoulTZ).Format("2006-01-02 15:04:05.000"), relativeTime(ts))
}

func relativeTime(ts time.Time) string {
	d := ts.Sub(nowFunc())
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

func displayAccountID(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "@") {
		return maskEmail(s)
	}
	if len(s) <= 12 {
		return s
	}
	return s[:8] + "…" + s[len(s)-4:]
}

func maskEmail(s string) string {
	at := strings.LastIndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return displayAccountIDFallback(s)
	}
	local := s[:at]
	domain := s[at+1:]
	localMask := local[:1] + "***"
	domainParts := strings.Split(domain, ".")
	if len(domainParts) == 0 || domainParts[0] == "" {
		return localMask + "@***"
	}
	hostMask := domainParts[0][:1] + "***"
	if len(domainParts) == 1 {
		return localMask + "@" + hostMask
	}
	return localMask + "@" + hostMask + "." + domainParts[len(domainParts)-1]
}

func displayAccountIDFallback(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:8] + "…" + s[len(s)-4:]
}
