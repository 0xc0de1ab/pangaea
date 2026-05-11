package router

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/notifier/telegram"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

const (
	maxNotifierHistory        = 256
	defaultRouterNotifyPeriod = time.Hour
	minRouterNotifyPeriod     = time.Minute
	telegramCommandPollTO     = 25
	telegramCommandRetryDelay = 5 * time.Second
)

type RouterNotifierOptions struct {
	Telegram RouterTelegramNotifierOptions
	Interval time.Duration
}

type RouterTelegramNotifierOptions struct {
	Enabled             bool
	BotToken            string
	ChatID              string
	Endpoint            string
	DisableNotification bool
}

type NotifierStatus struct {
	ID            string    `json:"id"`
	Type          string    `json:"type"`
	Destination   string    `json:"destination,omitempty"`
	Enabled       bool      `json:"enabled"`
	State         string    `json:"state"`
	Reason        string    `json:"reason,omitempty"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt time.Time `json:"last_success_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	SentCount     int       `json:"sent_count,omitempty"`
	FailedCount   int       `json:"failed_count,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type NotifierDelivery struct {
	ID          string    `json:"id"`
	NotifierID  string    `json:"notifier_id"`
	Type        string    `json:"type"`
	Destination string    `json:"destination,omitempty"`
	Status      string    `json:"status"`
	Message     string    `json:"message,omitempty"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

func StartRouterNotifiers(ctx context.Context, engine *Engine, opts RouterNotifierOptions) func() {
	if engine == nil {
		return func() {}
	}
	opts.Interval = normalizeRouterNotifyInterval(opts.Interval)
	notifiers := make([]routerNotifier, 0, 1)
	if opts.Telegram.Enabled {
		notifiers = append(notifiers, newRouterTelegramNotifier(opts.Telegram))
	} else {
		engine.UpsertNotifierStatus(NotifierStatus{
			ID:          "telegram",
			Type:        "telegram",
			Destination: redactNotifierDestination(opts.Telegram.ChatID),
			Enabled:     false,
			State:       "disabled",
			Reason:      "telegram notifier is not enabled",
			UpdatedAt:   time.Now().UTC(),
		})
	}
	if len(notifiers) == 0 {
		return func() {}
	}
	notifierCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, notifier := range notifiers {
			notifier.register(engine)
		}
		runRouterNotifierTick(notifierCtx, engine, notifiers, "startup")
		ticker := time.NewTicker(opts.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-notifierCtx.Done():
				return
			case <-ticker.C:
				runRouterNotifierTick(notifierCtx, engine, notifiers, "periodic")
			}
		}
	}()
	for _, notifier := range notifiers {
		commandNotifier, ok := notifier.(routerCommandNotifier)
		if !ok {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			commandNotifier.runCommands(notifierCtx, engine)
		}()
	}
	return func() {
		cancel()
		wg.Wait()
	}
}

type routerNotifier interface {
	register(engine *Engine)
	send(context.Context, *Engine, string) NotifierDelivery
}

type routerCommandNotifier interface {
	runCommands(context.Context, *Engine)
}

type routerTelegramNotifier struct {
	cfg    RouterTelegramNotifierOptions
	client *telegram.Client
}

func newRouterTelegramNotifier(cfg RouterTelegramNotifierOptions) *routerTelegramNotifier {
	client := telegram.New(cfg.BotToken)
	client.HTTP = &http.Client{Timeout: time.Duration(telegramCommandPollTO+5) * time.Second}
	if cfg.Endpoint != "" {
		client.Endpoint = cfg.Endpoint
	}
	return &routerTelegramNotifier{cfg: cfg, client: client}
}

func (n *routerTelegramNotifier) register(engine *Engine) {
	status := NotifierStatus{
		ID:          "telegram",
		Type:        "telegram",
		Destination: redactNotifierDestination(n.cfg.ChatID),
		Enabled:     n.cfg.Enabled,
		State:       "ready",
		UpdatedAt:   time.Now().UTC(),
	}
	switch {
	case strings.TrimSpace(n.cfg.BotToken) == "":
		status.State = "error"
		status.Reason = "telegram bot token is empty"
	case strings.TrimSpace(n.cfg.ChatID) == "":
		status.State = "error"
		status.Reason = "telegram chat id is empty"
	}
	engine.UpsertNotifierStatus(status)
}

func (n *routerTelegramNotifier) send(ctx context.Context, engine *Engine, deliveryType string) NotifierDelivery {
	now := time.Now().UTC()
	delivery := NotifierDelivery{
		NotifierID:  "telegram",
		Type:        deliveryType,
		Destination: redactNotifierDestination(n.cfg.ChatID),
		Status:      "failed",
		CreatedAt:   now,
	}
	if strings.TrimSpace(n.cfg.BotToken) == "" {
		delivery.Error = "telegram bot token is empty"
		delivery.CompletedAt = time.Now().UTC()
		return engine.RecordNotifierDelivery(delivery)
	}
	if strings.TrimSpace(n.cfg.ChatID) == "" {
		delivery.Error = "telegram chat id is empty"
		delivery.CompletedAt = time.Now().UTC()
		return engine.RecordNotifierDelivery(delivery)
	}
	text := renderRouterTelegramSummary(engine, deliveryType, now)
	err := n.client.SendMessage(ctx, telegram.SendMessageRequest{
		ChatID:              n.cfg.ChatID,
		Text:                text,
		ParseMode:           "HTML",
		DisableNotification: n.cfg.DisableNotification,
	})
	delivery.CompletedAt = time.Now().UTC()
	delivery.Message = routerNotificationMessageSummary(text)
	if err != nil {
		delivery.Error = err.Error()
		return engine.RecordNotifierDelivery(delivery)
	}
	delivery.Status = "sent"
	return engine.RecordNotifierDelivery(delivery)
}

func (n *routerTelegramNotifier) runCommands(ctx context.Context, engine *Engine) {
	if strings.TrimSpace(n.cfg.BotToken) == "" || strings.TrimSpace(n.cfg.ChatID) == "" {
		return
	}
	offset := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		updates, err := n.client.GetUpdates(ctx, telegram.GetUpdatesRequest{
			Offset:         offset,
			Timeout:        telegramCommandPollTO,
			AllowedUpdates: []string{"message", "channel_post"},
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(telegramCommandRetryDelay):
				continue
			}
		}
		for _, update := range updates {
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
			n.handleCommandUpdate(ctx, engine, update)
		}
	}
}

func (n *routerTelegramNotifier) handleCommandUpdate(ctx context.Context, engine *Engine, update telegram.Update) {
	msg := update.Message
	if msg == nil {
		msg = update.ChannelPost
	}
	if msg == nil {
		return
	}
	chatID := fmt.Sprintf("%d", msg.Chat.ID)
	if !n.commandChatAllowed(chatID) {
		return
	}
	cmd, ok := parseRouterTelegramCommand(msg.Text)
	if !ok {
		return
	}
	now := time.Now().UTC()
	text := renderRouterTelegramCommand(engine, cmd, now)
	if text == "" {
		return
	}
	delivery := NotifierDelivery{
		NotifierID:  "telegram",
		Type:        "command:" + cmd,
		Destination: redactNotifierDestination(chatID),
		Status:      "failed",
		CreatedAt:   now,
		Message:     routerNotificationMessageSummary(text),
	}
	err := n.client.SendMessage(ctx, telegram.SendMessageRequest{
		ChatID:              chatID,
		Text:                text,
		ParseMode:           "HTML",
		DisableNotification: n.cfg.DisableNotification,
		ReplyToMessageID:    msg.MessageID,
	})
	delivery.CompletedAt = time.Now().UTC()
	if err != nil {
		delivery.Error = err.Error()
		engine.RecordNotifierDelivery(delivery)
		return
	}
	delivery.Status = "sent"
	engine.RecordNotifierDelivery(delivery)
}

func (n *routerTelegramNotifier) commandChatAllowed(chatID string) bool {
	return strings.TrimSpace(n.cfg.ChatID) == strings.TrimSpace(chatID)
}

func runRouterNotifierTick(ctx context.Context, engine *Engine, notifiers []routerNotifier, deliveryType string) {
	for _, notifier := range notifiers {
		sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_ = notifier.send(sendCtx, engine, deliveryType)
		cancel()
	}
}

func normalizeRouterNotifyInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return defaultRouterNotifyPeriod
	}
	if interval < minRouterNotifyPeriod {
		return minRouterNotifyPeriod
	}
	return interval
}

func (e *Engine) UpsertNotifierStatus(status NotifierStatus) {
	if e == nil || strings.TrimSpace(status.ID) == "" {
		return
	}
	now := time.Now().UTC()
	if status.UpdatedAt.IsZero() {
		status.UpdatedAt = now
	}
	e.notifierMu.Lock()
	defer e.notifierMu.Unlock()
	if e.notifierStatuses == nil {
		e.notifierStatuses = make(map[string]NotifierStatus)
	}
	prev := e.notifierStatuses[status.ID]
	if !prev.LastAttemptAt.IsZero() && status.LastAttemptAt.IsZero() {
		status.LastAttemptAt = prev.LastAttemptAt
	}
	if !prev.LastSuccessAt.IsZero() && status.LastSuccessAt.IsZero() {
		status.LastSuccessAt = prev.LastSuccessAt
	}
	if status.SentCount == 0 {
		status.SentCount = prev.SentCount
	}
	if status.FailedCount == 0 {
		status.FailedCount = prev.FailedCount
	}
	if status.LastError == "" {
		status.LastError = prev.LastError
	}
	e.notifierStatuses[status.ID] = status
}

func (e *Engine) RecordNotifierDelivery(delivery NotifierDelivery) NotifierDelivery {
	if e == nil || strings.TrimSpace(delivery.NotifierID) == "" {
		return NotifierDelivery{}
	}
	now := time.Now().UTC()
	if delivery.CreatedAt.IsZero() {
		delivery.CreatedAt = now
	}
	if delivery.CompletedAt.IsZero() {
		delivery.CompletedAt = now
	}
	e.notifierMu.Lock()
	defer e.notifierMu.Unlock()
	if e.notifierStatuses == nil {
		e.notifierStatuses = make(map[string]NotifierStatus)
	}
	if strings.TrimSpace(delivery.ID) == "" {
		e.notifierSeq++
		delivery.ID = fmt.Sprintf("notif_%s_%06d", delivery.CreatedAt.UTC().Format("20060102150405.000000000"), e.notifierSeq)
	}
	if delivery.Status == "" {
		delivery.Status = "sent"
	}
	e.notifierHistory = append(e.notifierHistory, delivery)
	if len(e.notifierHistory) > maxNotifierHistory {
		e.notifierHistory = append([]NotifierDelivery(nil), e.notifierHistory[len(e.notifierHistory)-maxNotifierHistory:]...)
	}
	status := e.notifierStatuses[delivery.NotifierID]
	if status.ID == "" {
		status.ID = delivery.NotifierID
		status.Type = delivery.NotifierID
		status.Enabled = true
		status.Destination = delivery.Destination
	}
	status.LastAttemptAt = delivery.CreatedAt
	status.UpdatedAt = delivery.CompletedAt
	if delivery.Status == "sent" {
		status.State = "ready"
		status.LastSuccessAt = delivery.CompletedAt
		status.LastError = ""
		status.Reason = ""
		status.SentCount++
	} else {
		status.State = "error"
		status.LastError = delivery.Error
		status.Reason = delivery.Error
		status.FailedCount++
	}
	e.notifierStatuses[delivery.NotifierID] = status
	return delivery
}

func (e *Engine) NotifierStatuses() []NotifierStatus {
	if e == nil {
		return nil
	}
	e.notifierMu.RLock()
	defer e.notifierMu.RUnlock()
	out := make([]NotifierStatus, 0, len(e.notifierStatuses))
	for _, status := range e.notifierStatuses {
		out = append(out, status)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (e *Engine) NotifierHistory(limit int) []NotifierDelivery {
	if e == nil {
		return nil
	}
	if limit <= 0 || limit > maxNotifierHistory {
		limit = maxNotifierHistory
	}
	e.notifierMu.RLock()
	defer e.notifierMu.RUnlock()
	out := make([]NotifierDelivery, 0, min(limit, len(e.notifierHistory)))
	for i := len(e.notifierHistory) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, e.notifierHistory[i])
	}
	return out
}

func (e *Engine) notifierDeliveriesInRecordOrder(limit int) []NotifierDelivery {
	if e == nil {
		return nil
	}
	if limit <= 0 || limit > maxNotifierHistory {
		limit = maxNotifierHistory
	}
	e.notifierMu.RLock()
	defer e.notifierMu.RUnlock()
	start := 0
	if len(e.notifierHistory) > limit {
		start = len(e.notifierHistory) - limit
	}
	return append([]NotifierDelivery(nil), e.notifierHistory[start:]...)
}

func renderRouterTelegramSummary(engine *Engine, deliveryType string, now time.Time) string {
	providers := engine.Providers()
	authRecords := engine.AuthRecords()
	usages := engine.ProviderUsages()
	usagesByProvider := routerUsageByProvider(usages)
	readyProviders := 0
	authRisk := 0
	healthyAuth := 0
	for _, registration := range providers {
		if registration.Health.Status == provider.HealthReady {
			readyProviders++
		}
	}
	for _, record := range authRecords {
		switch record.Status {
		case provider.AuthHealthy, provider.AuthRefreshSoon, provider.AuthRefreshing:
			healthyAuth++
		default:
			authRisk++
		}
	}
	lines := []string{
		"Event: " + deliveryType,
		"Providers: " + fmt.Sprintf("%d ready / %d total", readyProviders, len(providers)),
		"Auth: " + fmt.Sprintf("%d ok / %d risk / %d total", healthyAuth, authRisk, len(authRecords)),
		"Usage sources: " + fmt.Sprintf("%d", len(usages)),
		"At: " + now.Local().Format("01-02 15:04:05"),
	}
	for _, record := range firstAuthRiskRecords(authRecords, 5) {
		lines = append(lines, fmt.Sprintf("- %s %s %s", record.Service, authAccountLabel(record), record.Status))
	}
	if quotaLines := routerProviderQuotaLines(providers, usagesByProvider, now, 8, 4); len(quotaLines) > 0 {
		lines = append(lines, "", "Quota:")
		lines = append(lines, quotaLines...)
	}
	return "<b>Pangaea Router</b>\n<pre>" + html.EscapeString(strings.Join(lines, "\n")) + "</pre>"
}

func renderRouterTelegramCommand(engine *Engine, cmd string, now time.Time) string {
	cmd = strings.ToLower(strings.TrimSpace(cmd))
	switch cmd {
	case "help", "start":
		return renderRouterTelegramHelp(engine)
	case "status", "providers":
		return renderRouterTelegramSummary(engine, "command:"+cmd, now)
	}
	providers := filterRouterCommandProviders(engine.Providers(), cmd)
	if len(providers) == 0 {
		return renderRouterTelegramUnknownCommand(engine, cmd)
	}
	return renderRouterTelegramProviderList(engine, cmd, providers, now)
}

func renderRouterTelegramHelp(engine *Engine) string {
	commands := []string{"help", "status"}
	for _, service := range routerCommandServices(engine.Providers()) {
		commands = append(commands, service)
	}
	lines := []string{
		"Pangaea Router Commands",
		"Use /status to retrieve all provider state.",
		"Use /<service> to retrieve providers and quota.",
		"",
		"Commands: /" + strings.Join(commands, ", /"),
	}
	if aliases := routerCommandAliases(engine.Providers()); len(aliases) > 0 {
		lines = append(lines, "Aliases: /"+strings.Join(aliases, ", /"))
	}
	return "<b>Pangaea Router</b>\n<pre>" + html.EscapeString(strings.Join(lines, "\n")) + "</pre>"
}

func renderRouterTelegramUnknownCommand(engine *Engine, cmd string) string {
	commands := append([]string{"help", "status"}, routerCommandServices(engine.Providers())...)
	lines := []string{
		fmt.Sprintf("Unknown command /%s", cmd),
		"Available: /" + strings.Join(commands, ", /"),
	}
	return "<b>Pangaea Router Error</b>\n<pre>" + html.EscapeString(strings.Join(lines, "\n")) + "</pre>"
}

func renderRouterTelegramProviderList(engine *Engine, cmd string, providers []provider.Registration, now time.Time) string {
	usages := routerUsageByProvider(engine.ProviderUsages())
	lines := []string{
		"Command: /" + cmd,
		"Providers: " + fmt.Sprintf("%d", len(providers)),
		"At: " + now.Local().Format("01-02 15:04:05"),
		"",
	}
	sort.SliceStable(providers, func(i, j int) bool {
		a := providers[i].Identity
		b := providers[j].Identity
		switch {
		case a.HostName != b.HostName:
			return a.HostName < b.HostName
		case routerProviderAccountLabel(providers[i]) != routerProviderAccountLabel(providers[j]):
			return routerProviderAccountLabel(providers[i]) < routerProviderAccountLabel(providers[j])
		default:
			return a.ProviderInstanceID < b.ProviderInstanceID
		}
	})
	for i, registration := range providers {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, routerProviderStatusLine(registration))
		windows := routerProviderQuotaWindows(registration, usages[registration.Identity.ProviderInstanceID])
		if len(windows) == 0 {
			lines = append(lines, "    quota not reported")
			continue
		}
		for _, window := range windows {
			lines = append(lines, routerQuotaWindowRows(window, now)...)
		}
	}
	title := fmt.Sprintf("Pangaea Providers · %s", routerCommandTitle(cmd))
	return "<b>" + html.EscapeString(title) + "</b>\n<pre>" + html.EscapeString(strings.Join(lines, "\n")) + "</pre>"
}

func routerProviderStatusLine(registration provider.Registration) string {
	identity := registration.Identity
	health := registration.Health.Status
	if health == "" {
		health = provider.HealthUnknown
	}
	auth := registration.Auth.Status
	if auth == "" {
		auth = provider.AuthUnknown
	}
	return truncateNotifierLine(fmt.Sprintf("- %s %s %s %s/%s",
		identity.ProviderInstanceID,
		routerProviderNodeLabel(registration),
		routerProviderAccountLabel(registration),
		health,
		auth,
	), 112)
}

func filterRouterCommandProviders(providers []provider.Registration, cmd string) []provider.Registration {
	cmd = normalizeRouterCommandService(cmd)
	out := make([]provider.Registration, 0, len(providers))
	for _, registration := range providers {
		identity := registration.Identity
		if normalizeRouterCommandService(string(identity.Service)) == cmd ||
			normalizeRouterCommandService(identity.ProviderType) == cmd ||
			strings.Contains(normalizeRouterCommandService(identity.ProviderType), cmd) {
			out = append(out, registration)
		}
	}
	return out
}

func routerCommandServices(providers []provider.Registration) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, registration := range providers {
		service := normalizeRouterCommandService(string(registration.Identity.Service))
		if service == "" {
			continue
		}
		if _, ok := seen[service]; ok {
			continue
		}
		seen[service] = struct{}{}
		out = append(out, service)
	}
	sort.Strings(out)
	return out
}

func routerCommandAliases(providers []provider.Registration) []string {
	services := map[string]struct{}{}
	for _, service := range routerCommandServices(providers) {
		services[service] = struct{}{}
	}
	aliases := []string{}
	if _, ok := services["antigravity"]; ok {
		aliases = append(aliases, "ag -> /antigravity")
	}
	return aliases
}

func normalizeRouterCommandService(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "/")
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "ag", "antigravity-sidecar":
		return "antigravity"
	case "codex-cli", "codex-appserver":
		return "codex"
	case "gemini-cli":
		return "gemini"
	case "claude-cli":
		return "claude"
	default:
		return value
	}
}

func routerCommandTitle(cmd string) string {
	switch normalizeRouterCommandService(cmd) {
	case "antigravity":
		return "Antigravity"
	case "codex":
		return "Codex"
	case "gemini":
		return "Gemini"
	case "claude":
		return "Claude"
	default:
		if cmd == "" {
			return "Providers"
		}
		return strings.ToUpper(cmd[:1]) + cmd[1:]
	}
}

func parseRouterTelegramCommand(text string) (string, bool) {
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

func routerUsageByProvider(usages []ProviderUsageSnapshot) map[string]ProviderUsageSnapshot {
	out := make(map[string]ProviderUsageSnapshot, len(usages))
	for _, usage := range usages {
		if strings.TrimSpace(usage.ProviderInstanceID) != "" {
			out[usage.ProviderInstanceID] = usage
		}
	}
	return out
}

type routerQuotaWindow struct {
	Label        string
	Used         int64
	Limit        int64
	RemainingPct float64
	Unit         string
	ResetAt      time.Time
	Source       string
}

func routerProviderQuotaLines(providers []provider.Registration, usages map[string]ProviderUsageSnapshot, now time.Time, providerLimit int, windowLimit int) []string {
	registrations := append([]provider.Registration(nil), providers...)
	sort.SliceStable(registrations, func(i, j int) bool {
		a := registrations[i].Identity
		b := registrations[j].Identity
		switch {
		case a.Service != b.Service:
			return a.Service < b.Service
		case a.HostName != b.HostName:
			return a.HostName < b.HostName
		case routerProviderAccountLabel(registrations[i]) != routerProviderAccountLabel(registrations[j]):
			return routerProviderAccountLabel(registrations[i]) < routerProviderAccountLabel(registrations[j])
		default:
			return a.ProviderInstanceID < b.ProviderInstanceID
		}
	})
	lines := []string{}
	omittedProviders := 0
	renderedProviders := 0
	for _, registration := range registrations {
		windows := routerProviderQuotaWindows(registration, usages[registration.Identity.ProviderInstanceID])
		if len(windows) == 0 {
			continue
		}
		if providerLimit > 0 && renderedProviders >= providerLimit {
			omittedProviders++
			continue
		}
		header := fmt.Sprintf("- %s %s %s",
			registration.Identity.Service,
			routerProviderNodeLabel(registration),
			routerProviderAccountLabel(registration),
		)
		lines = append(lines, truncateNotifierLine(header, 112))
		renderedProviders++
		renderedWindows := 0
		for _, window := range windows {
			if windowLimit > 0 && renderedWindows >= windowLimit {
				break
			}
			lines = append(lines, routerQuotaWindowRows(window, now)...)
			renderedWindows++
		}
		if hidden := len(windows) - renderedWindows; hidden > 0 {
			lines = append(lines, fmt.Sprintf("    ... +%d more quota windows", hidden))
		}
	}
	if omittedProviders > 0 {
		lines = append(lines, fmt.Sprintf("... +%d more providers with quota", omittedProviders))
	}
	return lines
}

func routerProviderQuotaWindows(registration provider.Registration, usage ProviderUsageSnapshot) []routerQuotaWindow {
	nativeWindows := routerNativeQuotaWindows(usage.Usage.NativeSummary)
	if routerIsGeminiRegistration(registration) {
		return routerGeminiQuotaWindows(registration.Models, nativeWindows)
	}
	return dedupeRouterQuotaWindows(append(nativeWindows, routerModelQuotaWindows(registration.Models)...))
}

func routerNativeQuotaWindows(raw any) []routerQuotaWindow {
	out := []routerQuotaWindow{}
	if native, ok := routerNativeUsageSummary(raw); ok {
		if len(native.Windows) > 0 {
			for _, window := range native.Windows {
				out = append(out, routerQuotaWindow{
					Label:        firstNonEmptyRouterString(window.Label, "Current window"),
					Used:         window.Used,
					Limit:        window.Limit,
					RemainingPct: window.RemainingPct,
					Unit:         window.Unit,
					ResetAt:      window.ResetAt,
					Source:       "usage",
				})
			}
		} else if routerUsageReportHasQuota(native) {
			out = append(out, routerQuotaWindow{
				Label:        "Current window",
				Used:         native.Used,
				Limit:        native.Limit,
				RemainingPct: native.RemainingPct,
				Unit:         native.Unit,
				ResetAt:      native.ResetAt,
				Source:       "usage",
			})
		}
	}
	return out
}

func routerModelQuotaWindows(models []provider.Model) []routerQuotaWindow {
	out := []routerQuotaWindow{}
	for _, model := range models {
		if model.Quota == nil {
			continue
		}
		out = append(out, routerQuotaWindow{
			Label:        firstNonEmptyRouterString(firstModelAlias(model), model.ID),
			RemainingPct: model.Quota.RemainingPct,
			ResetAt:      model.Quota.ResetAt,
			Source:       firstNonEmptyRouterString(model.Quota.Source, "model quota"),
		})
	}
	return out
}

func routerGeminiQuotaWindows(models []provider.Model, nativeWindows []routerQuotaWindow) []routerQuotaWindow {
	windows := make([]routerQuotaWindow, 0, len(nativeWindows)+len(models))
	for _, window := range nativeWindows {
		label := routerGeminiQuotaLabel(window.Label)
		if label == "" {
			continue
		}
		window.Label = label
		windows = append(windows, window)
	}
	for _, model := range models {
		if model.Quota == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(model.Kind), "group") {
			continue
		}
		label := routerGeminiModelQuotaLabel(model)
		if label == "" {
			continue
		}
		windows = append(windows, routerQuotaWindow{
			Label:        label,
			RemainingPct: model.Quota.RemainingPct,
			ResetAt:      model.Quota.ResetAt,
			Source:       firstNonEmptyRouterString(model.Quota.Source, "model quota"),
		})
	}
	return groupRouterGeminiQuotaWindows(windows)
}

func routerGeminiModelQuotaLabel(model provider.Model) string {
	values := []string{model.ID}
	values = append(values, model.Aliases...)
	values = append(values, model.GroupMembers...)
	for _, value := range values {
		if label := routerGeminiQuotaLabel(value); label != "" {
			return label
		}
	}
	return ""
}

func routerGeminiQuotaLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	switch {
	case strings.Contains(value, "flash-lite") || strings.Contains(value, "flash lite"):
		return "Flash Lite"
	case strings.Contains(value, "flash"):
		return "Flash"
	case strings.Contains(value, "pro"):
		return "Pro"
	default:
		return ""
	}
}

func groupRouterGeminiQuotaWindows(windows []routerQuotaWindow) []routerQuotaWindow {
	byLabel := map[string]routerQuotaWindow{}
	for _, window := range windows {
		label := routerGeminiQuotaLabel(window.Label)
		if label == "" {
			continue
		}
		window.Label = label
		current, ok := byLabel[label]
		if !ok || routerQuotaWindowLess(window, current) {
			byLabel[label] = window
		}
	}
	order := []string{"Flash", "Flash Lite", "Pro"}
	out := make([]routerQuotaWindow, 0, len(byLabel))
	for _, label := range order {
		window, ok := byLabel[label]
		if !ok {
			continue
		}
		out = append(out, window)
		delete(byLabel, label)
	}
	if len(byLabel) > 0 {
		rest := make([]string, 0, len(byLabel))
		for label := range byLabel {
			rest = append(rest, label)
		}
		sort.Strings(rest)
		for _, label := range rest {
			out = append(out, byLabel[label])
		}
	}
	return dedupeRouterQuotaWindows(out)
}

func routerQuotaWindowLess(candidate routerQuotaWindow, current routerQuotaWindow) bool {
	candidateRemaining := routerQuotaRemainingPct(candidate)
	currentRemaining := routerQuotaRemainingPct(current)
	if candidateRemaining != currentRemaining {
		return candidateRemaining < currentRemaining
	}
	switch {
	case candidate.ResetAt.IsZero():
		return false
	case current.ResetAt.IsZero():
		return true
	default:
		return candidate.ResetAt.Before(current.ResetAt)
	}
}

func routerIsGeminiRegistration(registration provider.Registration) bool {
	return normalizeRouterCommandService(string(registration.Identity.Service)) == "gemini" ||
		normalizeRouterCommandService(registration.Identity.ProviderType) == "gemini"
}

func routerNativeUsageSummary(raw any) (formats.UsageReport, bool) {
	if raw == nil {
		return formats.UsageReport{}, false
	}
	switch value := raw.(type) {
	case formats.UsageReport:
		return value, routerUsageReportHasQuota(value) || len(value.Windows) > 0
	case *formats.UsageReport:
		if value == nil {
			return formats.UsageReport{}, false
		}
		return *value, routerUsageReportHasQuota(*value) || len(value.Windows) > 0
	default:
		data, err := json.Marshal(raw)
		if err != nil {
			return formats.UsageReport{}, false
		}
		var out formats.UsageReport
		if err := json.Unmarshal(data, &out); err != nil {
			return formats.UsageReport{}, false
		}
		return out, routerUsageReportHasQuota(out) || len(out.Windows) > 0
	}
}

func routerUsageReportHasQuota(report formats.UsageReport) bool {
	return report.Limit > 0 || report.Used > 0 || report.RemainingPct > 0 || !report.ResetAt.IsZero()
}

func dedupeRouterQuotaWindows(windows []routerQuotaWindow) []routerQuotaWindow {
	out := make([]routerQuotaWindow, 0, len(windows))
	seen := map[string]struct{}{}
	for _, window := range windows {
		label := strings.TrimSpace(window.Label)
		if label == "" {
			label = "usage"
		}
		window.Label = label
		key := fmt.Sprintf("%s|%.3f|%s|%d|%d", label, window.RemainingPct, window.ResetAt.UTC().Format(time.RFC3339Nano), window.Used, window.Limit)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, window)
	}
	return out
}

func routerQuotaWindowRows(window routerQuotaWindow, now time.Time) []string {
	remaining := routerQuotaRemainingPct(window)
	bar := routerQuotaBar(remaining, 12)
	label := truncateNotifierLine(window.Label, 24)
	line := fmt.Sprintf("    %-24s %s %.0f%% left", label, bar, remaining)
	if window.Limit > 0 {
		unit := strings.TrimSpace(window.Unit)
		if unit == "" {
			unit = "units"
		}
		line += fmt.Sprintf(" (%s/%s %s)", compactNotifierNumber(window.Used), compactNotifierNumber(window.Limit), unit)
	}
	rows := []string{truncateNotifierLine(line, 112)}
	if !window.ResetAt.IsZero() {
		rows = append(rows, fmt.Sprintf("      reset %s (%s)", window.ResetAt.Local().Format("01-02 15:04"), routerTimeLeft(window.ResetAt, now)))
	}
	return rows
}

func routerQuotaRemainingPct(window routerQuotaWindow) float64 {
	remaining := window.RemainingPct
	if remaining <= 0 && window.Limit > 0 {
		left := window.Limit - window.Used
		if left < 0 {
			left = 0
		}
		remaining = float64(left) / float64(window.Limit) * 100
	}
	if remaining < 0 {
		return 0
	}
	if remaining > 100 {
		return 100
	}
	return remaining
}

func routerQuotaBar(remaining float64, width int) string {
	if width <= 0 {
		return ""
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

func routerTimeLeft(ts time.Time, now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	d := ts.Sub(now)
	if d < 0 {
		return humanNotifierDuration(-d) + " ago"
	}
	return "in " + humanNotifierDuration(d)
}

func humanNotifierDuration(d time.Duration) string {
	if d < time.Minute {
		return "less than 1m"
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
	if hours > 0 && len(parts) < 2 {
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

func firstModelAlias(model provider.Model) string {
	for _, alias := range model.Aliases {
		if strings.TrimSpace(alias) != "" {
			return strings.TrimSpace(alias)
		}
	}
	return ""
}

func routerProviderNodeLabel(registration provider.Registration) string {
	identity := registration.Identity
	if identity.NodeID != "" {
		return identity.NodeID
	}
	if identity.HostName != "" {
		return identity.HostName
	}
	return identity.ProviderInstanceID
}

func routerProviderAccountLabel(registration provider.Registration) string {
	if registration.Identity.Account.Display != "" {
		return registration.Identity.Account.Display
	}
	if registration.Identity.Account.ID != "" {
		return registration.Identity.Account.ID
	}
	if registration.Auth.Account.Display != "" {
		return registration.Auth.Account.Display
	}
	if registration.Auth.Account.ID != "" {
		return registration.Auth.Account.ID
	}
	return registration.Identity.ProviderInstanceID
}

func firstNonEmptyRouterString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func compactNotifierNumber(value int64) string {
	abs := value
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(value)/1_000_000_000)
	case abs >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(value)/1_000_000)
	case abs >= 1_000:
		return fmt.Sprintf("%.1fK", float64(value)/1_000)
	default:
		return fmt.Sprintf("%d", value)
	}
}

func truncateNotifierLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	if max == 1 {
		return s[:1]
	}
	return s[:max-1] + "…"
}

func firstAuthRiskRecords(records []AuthRecord, limit int) []AuthRecord {
	out := make([]AuthRecord, 0, limit)
	for _, record := range records {
		switch record.Status {
		case provider.AuthHealthy, provider.AuthRefreshSoon, provider.AuthRefreshing:
			continue
		}
		out = append(out, record)
		if len(out) >= limit {
			return out
		}
	}
	return out
}

func authAccountLabel(record AuthRecord) string {
	if record.Account.Display != "" {
		return record.Account.Display
	}
	if record.Account.ID != "" {
		return record.Account.ID
	}
	return record.ProviderInstanceID
}

func routerNotificationMessageSummary(text string) string {
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.TrimSpace(text)
	if len(text) <= 160 {
		return text
	}
	return text[:157] + "..."
}

func redactNotifierDestination(destination string) string {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return ""
	}
	if len(destination) <= 6 {
		return "***"
	}
	return destination[:3] + "..." + destination[len(destination)-3:]
}
