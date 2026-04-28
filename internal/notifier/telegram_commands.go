package notifier

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/internal/notifier/telegram"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

const (
	telegramCommandPollTimeout = 25
	telegramCommandRetryDelay  = 5 * time.Second
)

var telegramCommandRefreshWait = 1200 * time.Millisecond

type TelegramProfileRefresher func(ctx context.Context, profile string) (int, error)

type TelegramCommandConfig struct {
	Routes        []TelegramRoute
	DefaultChatID string
	ProbeTimeout  time.Duration
}

type TelegramCommandPoller struct {
	cfg        TelegramCommandConfig
	client     *telegram.Client
	truthSrc   TruthSource
	formats    FormatLookup
	httpClient *http.Client
	refresh    TelegramProfileRefresher
	log        *slog.Logger
}

func NewTelegramCommandPoller(
	cfg TelegramCommandConfig,
	client *telegram.Client,
	ts TruthSource,
	fl FormatLookup,
	httpClient *http.Client,
	refresh TelegramProfileRefresher,
	log *slog.Logger,
) *TelegramCommandPoller {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.ProbeTimeout <= 0 {
		cfg.ProbeTimeout = 8 * time.Second
	}
	return &TelegramCommandPoller{
		cfg:        cfg,
		client:     client,
		truthSrc:   ts,
		formats:    fl,
		httpClient: httpClient,
		refresh:    refresh,
		log: log.With(
			slog.String(logging.FieldComponent, "telegram-command"),
		),
	}
}

func (p *TelegramCommandPoller) Run(ctx context.Context) error {
	offset := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		updates, err := p.client.GetUpdates(ctx, telegram.GetUpdatesRequest{
			Offset:         offset,
			Timeout:        telegramCommandPollTimeout,
			AllowedUpdates: []string{"message", "channel_post"},
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			p.log.Warn("telegram command polling failed",
				slog.String(logging.FieldReason, err.Error()),
			)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(telegramCommandRetryDelay):
				continue
			}
		}
		for _, update := range updates {
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
			p.handleUpdate(ctx, update)
		}
	}
}

func (p *TelegramCommandPoller) handleUpdate(ctx context.Context, update telegram.Update) {
	msg := update.Message
	if msg == nil {
		msg = update.ChannelPost
	}
	if msg == nil {
		return
	}
	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	if !p.chatAllowed(chatID) {
		p.log.Debug("telegram command ignored from unconfigured chat",
			slog.String("chat_id", chatID),
		)
		return
	}
	cmd, ok := parseTelegramCommand(msg.Text)
	if !ok {
		return
	}
	text := p.commandResponse(ctx, cmd)
	if text == "" {
		return
	}
	if err := p.client.SendMessage(ctx, telegram.SendMessageRequest{
		ChatID:           chatID,
		Text:             text,
		ParseMode:        "HTML",
		ReplyToMessageID: msg.MessageID,
	}); err != nil {
		p.log.Warn("telegram command response failed",
			slog.String("chat_id", chatID),
			slog.String(logging.FieldReason, err.Error()),
		)
	}
}

func (p *TelegramCommandPoller) commandResponse(ctx context.Context, cmd string) string {
	records := p.truthSrc(ctx)
	profiles := profileNames(records)
	switch cmd {
	case "help":
		return telegramCommandHelp(profiles)
	case "status":
		return p.renderRecords(ctx, records)
	}
	if !slices.Contains(profiles, cmd) {
		return telegramError(fmt.Sprintf("Unknown profile %q.\nAvailable profiles: %s", cmd, strings.Join(profiles, ", ")))
	}
	if p.refresh != nil {
		if _, err := p.refresh(ctx, cmd); err != nil {
			p.log.Warn("telegram command snapshot request failed",
				slog.String(logging.FieldProfile, cmd),
				slog.String(logging.FieldReason, err.Error()),
			)
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(telegramCommandRefreshWait):
		}
		records = p.truthSrc(ctx)
	}
	filtered := filterProfileRecords(records, cmd)
	if len(filtered) == 0 {
		filtered = []TruthRecord{{Profile: cmd, NoTruth: true}}
	}
	return p.renderRecords(ctx, filtered)
}

func (p *TelegramCommandPoller) renderRecords(ctx context.Context, records []TruthRecord) string {
	items := make([]ReportRecord, 0, len(records))
	for _, r := range records {
		items = append(items, ReportRecord{Truth: r, Usage: p.probe(ctx, r)})
	}
	return renderPeriodicTelegram(items)
}

func (p *TelegramCommandPoller) probe(ctx context.Context, r TruthRecord) formats.UsageReport {
	if isSessionEvent(r) || r.NoTruth || r.RawB64 == "" {
		return formats.UsageReport{}
	}
	f, ok := p.formats(r.Format)
	if !ok {
		return formats.UsageReport{}
	}
	probe, ok := f.(formats.UsageProbe)
	if !ok {
		return formats.UsageReport{}
	}
	snap, err := decodeSnapshot(f, r.RawB64)
	if err != nil {
		return formats.UsageReport{}
	}
	probeCtx, cancel := context.WithTimeout(ctx, p.cfg.ProbeTimeout)
	defer cancel()
	rep, err := probe.Probe(probeCtx, snap, "", p.httpClient)
	if err != nil {
		p.log.Debug("usage probe failed",
			slog.String(logging.FieldProfile, r.Profile),
			slog.String(logging.FieldAccount, r.Account),
			slog.String(logging.FieldReason, err.Error()),
		)
		return formats.UsageReport{}
	}
	return rep
}

func (p *TelegramCommandPoller) chatAllowed(chatID string) bool {
	if strings.TrimSpace(p.cfg.DefaultChatID) == chatID {
		return true
	}
	for _, route := range p.cfg.Routes {
		if strings.TrimSpace(route.ChatID) == chatID {
			return true
		}
	}
	return false
}

func parseTelegramCommand(text string) (string, bool) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return "", false
	}
	cmd := strings.TrimPrefix(fields[0], "/")
	if i := strings.IndexByte(cmd, '@'); i >= 0 {
		cmd = cmd[:i]
	}
	cmd = strings.ToLower(strings.TrimSpace(cmd))
	if cmd == "" {
		return "", false
	}
	return cmd, true
}

func profileNames(records []TruthRecord) []string {
	names := make([]string, 0, len(records))
	for _, r := range records {
		if r.Profile == "" {
			continue
		}
		names = append(names, r.Profile)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func filterProfileRecords(records []TruthRecord, profile string) []TruthRecord {
	out := make([]TruthRecord, 0, len(records))
	for _, r := range records {
		if r.Profile == profile {
			out = append(out, r)
		}
	}
	return out
}

func telegramCommandHelp(profiles []string) string {
	lines := []string{
		"<b>Pangaea Commands</b>",
		"<pre>" + htmlEscape("Use /<profile> to retrieve current auth state.\nUse /status to retrieve all profiles.") + "</pre>",
	}
	if len(profiles) > 0 {
		lines = append(lines, "<pre>"+htmlEscape("Profiles: "+strings.Join(profiles, ", "))+"</pre>")
	}
	return strings.Join(lines, "\n")
}

func telegramError(msg string) string {
	return "<b>Pangaea Error</b>\n<pre>" + htmlEscape(msg) + "</pre>"
}
