package notifier

import (
	"context"
	"fmt"
	"strings"

	"github.com/0xc0de1ab/pangaea/internal/notifier/telegram"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// TelegramRoute pins one (profile, account) tuple to one Telegram chat.
// Empty fields act as wildcards.
type TelegramRoute struct {
	Profile string
	Account string
	ChatID  string
}

// TelegramSinkConfig configures the Telegram fan-out target.
type TelegramSinkConfig struct {
	Routes              []TelegramRoute
	DefaultChatID       string
	DisableNotification bool
}

// TelegramSink is a Sink that routes per (profile, account) to chat IDs
// and renders messages with HTML parse mode.
type TelegramSink struct {
	cfg      TelegramSinkConfig
	client   *telegram.Client
	periodic periodicState
}

// NewTelegramSink constructs a sink. Caller is responsible for setting
// client.BotToken and (optionally) client.Endpoint before passing it in.
func NewTelegramSink(cfg TelegramSinkConfig, client *telegram.Client) *TelegramSink {
	return &TelegramSink{cfg: cfg, client: client}
}

// Name returns the sink label used in logs.
func (s *TelegramSink) Name() string { return "telegram" }

// Notify resolves a chat for (profile, account) and posts an HTML message.
// Returns (false, nil) when no route matches and no default is set.
func (s *TelegramSink) Notify(ctx context.Context, r TruthRecord, u formats.UsageReport) (bool, error) {
	chatID := s.routeFor(r.Profile, r.Account)
	if chatID == "" {
		return false, nil
	}
	text := renderTelegram(r, u)
	return true, s.client.SendMessage(ctx, telegram.SendMessageRequest{
		ChatID:              chatID,
		Text:                text,
		ParseMode:           "HTML",
		DisableNotification: s.cfg.DisableNotification,
	})
}

func (s *TelegramSink) NotifySessionBatch(ctx context.Context, events []TruthRecord) error {
	groups := groupSessionEvents(events, s.routeFor)
	for _, chatID := range sortedTruthGroupKeys(groups) {
		if err := s.client.SendMessage(ctx, telegram.SendMessageRequest{
			ChatID:              chatID,
			Text:                renderSessionBatchTelegram(groups[chatID]),
			ParseMode:           "HTML",
			DisableNotification: s.cfg.DisableNotification,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *TelegramSink) NotifyPeriodic(ctx context.Context, records []ReportRecord) error {
	groups := groupPeriodicRecords(records, s.routeFor)
	for _, chatID := range sortedGroupKeys(groups) {
		signature := periodicDigest(groups[chatID])
		text := renderPeriodicTelegram(groups[chatID])
		if s.periodic.unchanged(chatID, signature) {
			continue
		}
		if err := s.client.SendMessage(ctx, telegram.SendMessageRequest{
			ChatID:              chatID,
			Text:                text,
			ParseMode:           "HTML",
			DisableNotification: s.cfg.DisableNotification,
		}); err != nil {
			return err
		}
		s.periodic.remember(chatID, signature)
	}
	return nil
}

func (s *TelegramSink) routeFor(profile, account string) string {
	for _, r := range s.cfg.Routes {
		if (r.Profile == "" || r.Profile == profile) &&
			(r.Account == "" || r.Account == account) {
			return r.ChatID
		}
	}
	return s.cfg.DefaultChatID
}

// renderTelegram composes the human-facing text in Telegram's HTML mode.
func renderTelegram(r TruthRecord, u formats.UsageReport) string {
	if r.EventKind == EventSessionConnected || r.EventKind == EventSessionDisconnected || r.EventKind == EventSessionReconnected {
		v := buildView(r, u)
		lines := []string{
			telegramKV("Profile", v.Profile),
			telegramKV("LLM", v.Model),
			telegramKV("Node", v.NodeID),
		}
		if v.AuthMode != "" {
			lines = append(lines, telegramKV("Auth Mode", v.AuthMode))
		}
		if v.PeerCN != "" {
			lines = append(lines, telegramKV("Peer CN", v.PeerCN))
		}
		if v.HasNodes {
			lines = append(lines, telegramKV("Connected", strings.Join(v.Nodes, ", ")))
		}
		return strings.Join([]string{
			telegramHTMLBold(v.Heading),
			"<pre>" + htmlEscape(strings.Join(lines, "\n")) + "</pre>",
		}, "\n")
	}

	v := buildView(r, u)
	lines := []string{
		telegramHTMLBold(v.Heading),
		"<pre>" + htmlEscape(strings.Join(renderTelegramTableLines(v), "\n")) + "</pre>",
	}
	return strings.Join(lines, "\n")
}

func renderPeriodicTelegram(records []ReportRecord) string {
	lines := []string{telegramHTMLBold("Auth State")}
	seqByProfile := map[string]int{}
	for _, record := range records {
		v := buildPeriodicView(record)
		seqByProfile[v.Profile]++
		if len(lines) > 1 {
			lines = append(lines, "")
		}
		lines = append(lines, renderPeriodicTelegramAccountTitle(v, seqByProfile[v.Profile]))
		lines = append(lines, renderPeriodicTelegramMetaLines(v)...)
		if usageLines := renderPeriodicTelegramUsageBlock(v); len(usageLines) > 0 {
			lines = append(lines, "<pre>"+htmlEscape(strings.Join(usageLines, "\n"))+"</pre>")
		}
	}
	return strings.Join(lines, "\n")
}

func renderSessionBatchTelegram(events []TruthRecord) string {
	v := buildSessionBatchView(events)
	lines := []string{}
	lines = append(lines, telegramKV("Node", v.NodeID))
	if v.AuthMode != "" {
		lines = append(lines, telegramKV("Auth Mode", v.AuthMode))
	}
	if v.PeerCN != "" {
		lines = append(lines, telegramKV("Peer CN", v.PeerCN))
	}
	lines = append(lines, "")
	lines = append(lines, sessionBatchTableLines(v)...)
	return strings.Join([]string{
		telegramHTMLBold(v.Heading),
		"<pre>" + htmlEscape(strings.Join(lines, "\n")) + "</pre>",
	}, "\n")
}

func renderTelegramTableLines(v renderView) []string {
	rows := [][2]string{
		{"Profile", v.Profile},
		{"LLM", v.Model},
	}
	if v.HasNodes {
		rows = append(rows, [2]string{"Nodes", strings.Join(v.Nodes, ", ")})
	}
	if v.HasSourceNode {
		rows = append(rows, [2]string{"Latest Detected", v.SourceNode})
	}
	if v.HasTargetNodes {
		rows = append(rows, [2]string{"Propagated To", strings.Join(v.TargetNodes, ", ")})
	}
	if v.HasAccount {
		rows = append(rows, [2]string{"Account", v.AccountLabel})
	}
	if v.HasLastRefresh {
		rows = append(rows, [2]string{"Refresh", v.LastRefresh})
	}
	if v.HasExpiryChange {
		rows = append(rows, [2]string{"Expiry Change", v.ExpiryChange})
	} else if v.HasValidity {
		rows = append(rows, [2]string{"Valid Until", v.Validity})
	}
	if v.HasPlan {
		rows = append(rows, [2]string{"Plan", v.Plan})
	}
	if v.HasFingerprint {
		rows = append(rows, [2]string{"Fingerprint", v.Fingerprint})
	}
	lines := make([]string, 0, len(rows)+len(v.UsageDetails)+len(v.Notes)+4)
	for _, row := range rows {
		lines = append(lines, telegramKV(row[0], row[1]))
	}
	if v.HasUsage {
		lines = append(lines, telegramKV("Usage", v.Usage))
	}
	if len(v.UsageDetails) > 0 {
		lines = append(lines, "")
		lines = append(lines, "USAGE")
		for _, detail := range v.UsageDetails {
			lines = append(lines, telegramBarRows(detail)...)
		}
	}
	if len(v.Notes) > 0 {
		lines = append(lines, "")
		lines = append(lines, "NOTES")
		for _, note := range v.Notes {
			lines = append(lines, "  "+truncateTelegram(note, 92))
		}
	}
	return lines
}

func renderPeriodicTelegramAccountTitle(v periodicView, seq int) string {
	profile := v.Profile
	if profile == "" {
		profile = "profile"
	}
	title := fmt.Sprintf("%s #%d", profile, seq)
	if v.NoTruth {
		return fmt.Sprintf("%s - ❌ no truth", telegramHTMLBold(title))
	}
	account := v.AccountLabel
	if account == "" || account == "-" {
		account = "unknown account"
	}
	valid := v.ValidUntil
	if valid == "" || valid == "-" {
		return fmt.Sprintf("%s - %s", telegramHTMLBold(title), telegramHTMLCode(account))
	}
	return fmt.Sprintf("%s - %s - %s", telegramHTMLBold(title), telegramHTMLCode(account), htmlEscape(valid))
}

func renderPeriodicTelegramMetaLines(v periodicView) []string {
	lines := make([]string, 0, 3)
	if len(v.Nodes) > 0 {
		lines = append(lines, "nodes: "+telegramHTMLCode(strings.Join(v.Nodes, ",")))
	}
	if v.LastRefresh != "" && v.LastRefresh != "-" {
		lines = append(lines, "last refresh: "+htmlEscape(v.LastRefresh))
	}
	if v.NoTruth && len(v.Nodes) == 0 {
		lines = append(lines, "nodes: "+telegramHTMLCode("-"))
	}
	return lines
}

func renderPeriodicTelegramUsageBlock(v periodicView) []string {
	lines := make([]string, 0, len(v.UsageDetails)*2)
	for _, detail := range v.UsageDetails {
		lines = append(lines, telegramBarRows(detail)...)
	}
	return lines
}

func telegramKV(label, value string) string {
	return fmt.Sprintf("%-8s %s", label, truncateTelegram(value, 96))
}

func telegramBarRows(detail string) []string {
	label, pct, reset := splitUsageDetail(detail)
	bar := usageBarFromDetail(detail, 12)
	if pct == "" {
		return []string{"  " + truncateTelegram(detail, 96)}
	}
	line1 := strings.TrimSpace(fmt.Sprintf("%-24s %s", truncateTelegram(label, 24), bar+" "+pct))
	if reset == "" {
		return []string{"  " + line1}
	}
	line2 := "      " + truncateTelegram(reset, 48)
	return []string{"  " + line1, line2}
}

func splitUsageDetail(detail string) (label, pct, reset string) {
	parts := strings.SplitN(detail, ": ", 2)
	if len(parts) == 2 {
		label = parts[0]
		detail = parts[1]
	} else {
		label = "usage"
	}
	if idx := strings.Index(detail, "% left"); idx >= 0 {
		pct = detail[:idx+6]
		if next := strings.Index(detail[idx+6:], "resets "); next >= 0 {
			reset = strings.TrimSpace(detail[idx+6+next+7:])
		}
	}
	return strings.TrimSpace(label), strings.TrimSpace(pct), strings.TrimSpace(reset)
}

func usageBarFromDetail(detail string, width int) string {
	_, pct, _ := splitUsageDetail(detail)
	if pct == "" {
		return ""
	}
	var remaining float64
	if _, err := fmt.Sscanf(pct, "%f%% left", &remaining); err != nil {
		return ""
	}
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 100 {
		remaining = 100
	}
	filled := int((remaining / 100) * float64(width))
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func truncateTelegram(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	if max == 1 {
		return s[:1]
	}
	return s[:max-1] + "…"
}

func telegramHTMLBold(s string) string {
	return "<b>" + htmlEscape(s) + "</b>"
}

func telegramHTMLCode(s string) string {
	return "<code>" + htmlEscape(s) + "</code>"
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func shortFP(fp string) string {
	if len(fp) > 12 {
		return fp[:12]
	}
	return fp
}
