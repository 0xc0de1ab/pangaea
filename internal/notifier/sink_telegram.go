package notifier

import (
	"context"
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
	cfg    TelegramSinkConfig
	client *telegram.Client
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
	return renderHTML(r, u)
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

func shortAccount(a string) string {
	if a == "" {
		return "<no-account>"
	}
	if len(a) > 24 {
		return a[:24] + "…"
	}
	return a
}
