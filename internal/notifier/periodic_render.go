package notifier

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

var seoulTZ = mustLoadLocation("Asia/Seoul", 9*60*60)

type periodicMarkup struct {
	escape  func(string) string
	bold    func(string) string
	lineEnd string
}

type periodicView struct {
	TruthStatus  string
	NoTruth      bool
	Profile      string
	Nodes        []string
	ValidUntil   string
	AccountLabel string
	LastRefresh  string
	UsageDetails []string
}

func renderPeriodicPlain(records []ReportRecord) string {
	return renderPeriodicRich(records, periodicMarkup{
		escape:  func(s string) string { return s },
		bold:    func(s string) string { return s },
		lineEnd: "\n",
	})
}

func renderPeriodicMarkdown(records []ReportRecord) string {
	return renderPeriodicRich(records, periodicMarkup{
		escape:  mdEscape,
		bold:    func(s string) string { return "**" + s + "**" },
		lineEnd: "\n",
	})
}

func renderPeriodicMrkdwn(records []ReportRecord) string {
	return renderPeriodicRich(records, periodicMarkup{
		escape:  mrkdwnEscape,
		bold:    func(s string) string { return "*" + s + "*" },
		lineEnd: "\n",
	})
}

func renderPeriodicHTML(records []ReportRecord) string {
	return renderPeriodicRich(records, periodicMarkup{
		escape:  htmlEscape,
		bold:    func(s string) string { return "<b>" + s + "</b>" },
		lineEnd: "\n",
	})
}

func renderPeriodicTeams(records []ReportRecord) string {
	return renderPeriodicRich(records, periodicMarkup{
		escape:  mdEscape,
		bold:    func(s string) string { return "**" + s + "**" },
		lineEnd: "  \n",
	})
}

func renderPeriodicRich(records []ReportRecord, m periodicMarkup) string {
	lines := []string{m.bold(m.escape("Auth State"))}
	for i, record := range records {
		if i > 0 {
			lines = append(lines, "---")
		}
		v := buildPeriodicView(record)
		profile := v.Profile
		if len(v.Nodes) > 0 {
			profile += " (" + strings.Join(v.Nodes, ", ") + ")"
		}
		lines = append(lines, "Profile: "+m.escape(profile))
		lines = append(lines, "Truth: "+m.escape(v.TruthStatus))
		lines = append(lines, "Valid Until: "+m.escape(v.ValidUntil))
		if v.AccountLabel != "" && v.AccountLabel != "-" {
			lines = append(lines, "Account: "+m.escape(v.AccountLabel))
		}
		lines = append(lines, "Last Refresh: "+m.escape(v.LastRefresh))
		for _, detail := range v.UsageDetails {
			if detail == "" {
				continue
			}
			lines = append(lines, "Usage: "+m.escape(detail))
		}
	}
	return strings.Join(lines, m.lineEnd)
}

func buildPeriodicView(record ReportRecord) periodicView {
	sum := parseSummary(record.Truth.Summary)
	nodes := normalizedNodes(record.Truth)
	accountLabel := periodicAccountLabel(sum)

	lastRefresh := "-"
	if ts, ok := parseSummaryTime(sum.Extra["last_refresh"]); ok {
		lastRefresh = seoulTimeLine(ts, false)
	}

	validUntil := "-"
	if !sum.ExpiresAt.IsZero() {
		validUntil = seoulTimeLine(sum.ExpiresAt, true)
	}

	truthStatus := "✅"
	if record.Truth.NoTruth {
		truthStatus = "❌"
	}

	return periodicView{
		TruthStatus:  truthStatus,
		NoTruth:      record.Truth.NoTruth,
		Profile:      record.Truth.Profile,
		Nodes:        nodes,
		ValidUntil:   validUntil,
		AccountLabel: accountLabel,
		LastRefresh:  lastRefresh,
		UsageDetails: usageDetailLines(record.Usage),
	}
}

func periodicAccountLabel(sum renderSummary) string {
	for _, raw := range []string{
		sum.Extra["display_account"],
		sum.Extra["email"],
		sum.Extra["masked_email"],
	} {
		raw = strings.TrimSpace(raw)
		if raw == "" || raw == "-" {
			continue
		}
		return displayAccountLabel(raw)
	}
	return "-"
}

func normalizedNodes(r TruthRecord) []string {
	nodes := append([]string(nil), r.Nodes...)
	if len(nodes) == 0 && r.SourceNode != "" {
		nodes = append(nodes, r.SourceNode)
	}
	for i := range nodes {
		nodes[i] = strings.TrimSpace(nodes[i])
	}
	nodes = slices.DeleteFunc(nodes, func(s string) bool { return s == "" })
	slices.Sort(nodes)
	nodes = slices.Compact(nodes)
	return nodes
}

func parseSummaryTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	layouts := []string{time.RFC3339Nano, time.RFC3339}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
}

func seoulTimeLine(ts time.Time, withRelative bool) string {
	if ts.IsZero() {
		return "-"
	}
	base := ts.In(seoulTZ).Format("2006-01-02 15:04:05.000")
	if !withRelative {
		return base
	}
	return fmt.Sprintf("%s (%s)", base, relativeTime(ts))
}

func mustLoadLocation(name string, fallbackOffsetSeconds int) *time.Location {
	loc, err := time.LoadLocation(name)
	if err == nil {
		return loc
	}
	return time.FixedZone(name, fallbackOffsetSeconds)
}
