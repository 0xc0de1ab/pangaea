package router

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/notifier/telegram"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

const (
	maxNotifierHistory        = 1000
	defaultRouterNotifyPeriod = time.Hour
	minRouterNotifyPeriod     = time.Minute
	defaultRouterStartupGrace = 45 * time.Second
	routerNotifierSendTimeout = 30 * time.Second
	telegramMessageSoftLimit  = 3800
	telegramCommandPollTO     = 25
	telegramCommandRetryDelay = 5 * time.Second
)

var telegramBotTokenPattern = regexp.MustCompile(`bot[0-9]{6,}:[A-Za-z0-9_-]{20,}|[0-9]{6,}:[A-Za-z0-9_-]{20,}`)

type RouterNotifierOptions struct {
	Telegram     RouterTelegramNotifierOptions
	Interval     time.Duration
	StartupGrace time.Duration
}

type RouterTelegramNotifierOptions struct {
	Enabled             bool
	BotToken            string
	ChatID              string
	Endpoint            string
	DisableNotification bool
}

type NotifierStatus struct {
	ID             string    `json:"id"`
	Type           string    `json:"type"`
	Destination    string    `json:"destination,omitempty"`
	Enabled        bool      `json:"enabled"`
	State          string    `json:"state"`
	Reason         string    `json:"reason,omitempty"`
	LastAttemptAt  time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt  time.Time `json:"last_success_at,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	SentCount      int       `json:"sent_count,omitempty"`
	FailedCount    int       `json:"failed_count,omitempty"`
	LastAutoDigest string    `json:"last_auto_digest,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
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
	opts.StartupGrace = normalizeRouterStartupGrace(opts.StartupGrace)
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
		waitRouterNotifierStartupReady(notifierCtx, engine, opts.StartupGrace)
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

func normalizeRouterStartupGrace(value time.Duration) time.Duration {
	if value < 0 {
		return 0
	}
	if value == 0 {
		return defaultRouterStartupGrace
	}
	return value
}

func waitRouterNotifierStartupReady(ctx context.Context, engine *Engine, timeout time.Duration) {
	if engine == nil || timeout <= 0 || routerNotifierHasState(engine) {
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if routerNotifierHasState(engine) {
				return
			}
		case <-timer.C:
			return
		}
	}
}

func routerNotifierHasState(engine *Engine) bool {
	return len(engine.Providers()) > 0 || len(engine.AuthRecords()) > 0
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

	periodicMu   sync.Mutex
	periodicLast map[string]string
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
	if routerTelegramDeliveryShouldRenderProviderAccounts(deliveryType) {
		return n.sendProviderAccountBatch(ctx, engine, deliveryType, now)
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
		delivery.Error = scrubRouterNotifierSecret(err.Error(), n.cfg.BotToken)
		return engine.RecordNotifierDelivery(delivery)
	}
	delivery.Status = "sent"
	return engine.RecordNotifierDelivery(delivery)
}

func (n *routerTelegramNotifier) sendProviderAccountBatch(ctx context.Context, engine *Engine, deliveryType string, now time.Time) NotifierDelivery {
	groups := routerProviderAccountGroups(engine.Providers(), routerUsageByProvider(engine.ProviderUsages()))
	var accountDigest string
	var storedDigest string
	if routerTelegramDeliveryShouldTrackDigest(deliveryType) {
		accountDigest = routerProviderAccountDigest(groups)
		storedDigest = routerNotifierAutoDigest(n.cfg.ChatID, accountDigest)
		if routerTelegramDeliveryShouldDeduplicate(deliveryType) && (n.periodicDigestUnchanged(n.cfg.ChatID, accountDigest) || engine.NotifierAutoDigestUnchanged("telegram", storedDigest)) {
			return NotifierDelivery{}
		}
	}
	if len(groups) == 0 && len(engine.AuthRecords()) == 0 && routerTelegramDeliveryShouldTrackDigest(deliveryType) {
		n.rememberPeriodicDigest(n.cfg.ChatID, accountDigest)
		engine.RememberNotifierAutoDigest("telegram", storedDigest)
		return NotifierDelivery{}
	}
	if strings.EqualFold(strings.TrimSpace(deliveryType), "startup") {
		n.rememberPeriodicDigest(n.cfg.ChatID, accountDigest)
		engine.RememberNotifierAutoDigest("telegram", storedDigest)
		return NotifierDelivery{}
	}
	var messages []string
	messages = renderRouterTelegramProviderAccountBatchMessages(groups, deliveryType, now)
	if len(messages) == 0 {
		messages = []string{renderRouterTelegramSummary(engine, deliveryType, now)}
	}
	var last NotifierDelivery
	for _, text := range messages {
		item := NotifierDelivery{
			NotifierID:  "telegram",
			Type:        deliveryType,
			Destination: redactNotifierDestination(n.cfg.ChatID),
			Status:      "failed",
			CreatedAt:   time.Now().UTC(),
			Message:     routerNotificationMessageSummary(text),
		}
		err := n.client.SendMessage(ctx, telegram.SendMessageRequest{
			ChatID:              n.cfg.ChatID,
			Text:                text,
			ParseMode:           "HTML",
			DisableNotification: n.cfg.DisableNotification,
		})
		item.CompletedAt = time.Now().UTC()
		if err != nil {
			item.Error = scrubRouterNotifierSecret(err.Error(), n.cfg.BotToken)
			last = engine.RecordNotifierDelivery(item)
			return last
		}
		item.Status = "sent"
		last = engine.RecordNotifierDelivery(item)
	}
	if accountDigest != "" {
		n.rememberPeriodicDigest(n.cfg.ChatID, accountDigest)
		engine.RememberNotifierAutoDigest("telegram", storedDigest)
	}
	return last
}

func routerTelegramDeliveryShouldRenderProviderAccounts(deliveryType string) bool {
	switch strings.ToLower(strings.TrimSpace(deliveryType)) {
	case "startup", "periodic":
		return true
	default:
		return false
	}
}

func routerTelegramDeliveryShouldDeduplicate(deliveryType string) bool {
	switch strings.ToLower(strings.TrimSpace(deliveryType)) {
	case "startup", "periodic":
		return true
	default:
		return false
	}
}

func routerTelegramDeliveryShouldTrackDigest(deliveryType string) bool {
	switch strings.ToLower(strings.TrimSpace(deliveryType)) {
	case "startup", "periodic":
		return true
	default:
		return false
	}
}

func (n *routerTelegramNotifier) periodicDigestUnchanged(destination string, digest string) bool {
	n.periodicMu.Lock()
	defer n.periodicMu.Unlock()
	if n.periodicLast == nil {
		n.periodicLast = map[string]string{}
	}
	return n.periodicLast[destination] == digest
}

func (n *routerTelegramNotifier) rememberPeriodicDigest(destination string, digest string) {
	n.periodicMu.Lock()
	defer n.periodicMu.Unlock()
	if n.periodicLast == nil {
		n.periodicLast = map[string]string{}
	}
	n.periodicLast[destination] = digest
}

func routerNotifierAutoDigest(destination string, digest string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(destination) + "\x00" + digest))
	return hex.EncodeToString(sum[:])
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
		delivery.Error = scrubRouterNotifierSecret(err.Error(), n.cfg.BotToken)
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
		sendCtx, cancel := context.WithTimeout(ctx, routerNotifierSendTimeout)
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
	status = sanitizeNotifierStatus(status)
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
	if status.LastAutoDigest == "" {
		status.LastAutoDigest = prev.LastAutoDigest
	}
	status = sanitizeNotifierStatus(status)
	e.notifierStatuses[status.ID] = status
}

func (e *Engine) NotifierAutoDigestUnchanged(notifierID string, digest string) bool {
	if e == nil || strings.TrimSpace(notifierID) == "" || strings.TrimSpace(digest) == "" {
		return false
	}
	e.notifierMu.RLock()
	defer e.notifierMu.RUnlock()
	status := e.notifierStatuses[notifierID]
	return status.LastAutoDigest == digest
}

func (e *Engine) RememberNotifierAutoDigest(notifierID string, digest string) {
	if e == nil || strings.TrimSpace(notifierID) == "" || strings.TrimSpace(digest) == "" {
		return
	}
	now := time.Now().UTC()
	e.notifierMu.Lock()
	defer e.notifierMu.Unlock()
	if e.notifierStatuses == nil {
		e.notifierStatuses = make(map[string]NotifierStatus)
	}
	status := e.notifierStatuses[notifierID]
	if status.ID == "" {
		status.ID = notifierID
		status.Type = notifierID
	}
	status.LastAutoDigest = digest
	if status.UpdatedAt.IsZero() {
		status.UpdatedAt = now
	}
	e.notifierStatuses[notifierID] = sanitizeNotifierStatus(status)
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
	delivery = sanitizeNotifierDelivery(delivery)
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

type routerProviderAccountGroup struct {
	Service     string
	ServiceKey  string
	Account     string
	Providers   []provider.Registration
	Nodes       []string
	ProviderIDs []string
	Health      []string
	Auth        []string
	Versions    []string
	Windows     []routerQuotaWindow
}

type routerProviderAccountDigestRecord struct {
	ServiceKey  string   `json:"service_key"`
	Account     string   `json:"account"`
	Nodes       []string `json:"nodes,omitempty"`
	ProviderIDs []string `json:"provider_ids,omitempty"`
	Health      []string `json:"health,omitempty"`
	Auth        []string `json:"auth,omitempty"`
	Versions    []string `json:"versions,omitempty"`
}

func renderRouterTelegramProviderAccountMessages(engine *Engine, deliveryType string, now time.Time) []string {
	if engine == nil {
		return nil
	}
	providers := engine.Providers()
	usages := routerUsageByProvider(engine.ProviderUsages())
	groups := routerProviderAccountGroups(providers, usages)
	if len(groups) == 0 {
		return nil
	}
	seqByService := map[string]int{}
	messages := make([]string, 0, len(groups))
	for _, group := range groups {
		seqByService[group.ServiceKey]++
		messages = append(messages, renderRouterTelegramProviderAccountMessage(group, seqByService[group.ServiceKey], deliveryType, now))
	}
	return messages
}

func renderRouterTelegramProviderAccountBatchMessages(groups []routerProviderAccountGroup, deliveryType string, now time.Time) []string {
	if len(groups) == 0 {
		return nil
	}
	seqByService := map[string]int{}
	blocks := make([][]string, 0, len(groups))
	for _, group := range groups {
		seqByService[group.ServiceKey]++
		blocks = append(blocks, routerProviderAccountBlockLines(group, seqByService[group.ServiceKey], deliveryType, now, false))
	}
	base := []string{
		"event: " + deliveryType,
		fmt.Sprintf("accounts: %d", len(groups)),
		"at: " + now.Local().Format("01-02 15:04:05"),
		"",
	}
	heading := "Pangaea Router · Usage"
	messages := []string{}
	current := append([]string(nil), base...)
	currentHasBlock := false
	for _, block := range blocks {
		candidate := append(append([]string(nil), current...), block...)
		candidate = append(candidate, "")
		if currentHasBlock && len(routerTelegramBatchMessage(heading, candidate)) > telegramMessageSoftLimit {
			messages = append(messages, routerTelegramBatchMessage(heading, trimTrailingEmptyRouterLines(current)))
			current = append(append([]string(nil), base...), block...)
			current = append(current, "")
			continue
		}
		current = candidate
		currentHasBlock = true
	}
	if currentHasBlock {
		messages = append(messages, routerTelegramBatchMessage(heading, trimTrailingEmptyRouterLines(current)))
	}
	return messages
}

func routerTelegramBatchMessage(heading string, lines []string) string {
	return "<b>" + html.EscapeString(heading) + "</b>\n<pre>" + html.EscapeString(strings.Join(lines, "\n")) + "</pre>"
}

func trimTrailingEmptyRouterLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func routerProviderAccountDigest(groups []routerProviderAccountGroup) string {
	records := make([]routerProviderAccountDigestRecord, 0, len(groups))
	for _, group := range groups {
		records = append(records, routerProviderAccountDigestRecord{
			ServiceKey:  group.ServiceKey,
			Account:     group.Account,
			Nodes:       append([]string(nil), group.Nodes...),
			ProviderIDs: append([]string(nil), group.ProviderIDs...),
			Health:      append([]string(nil), group.Health...),
			Auth:        append([]string(nil), group.Auth...),
			Versions:    append([]string(nil), group.Versions...),
		})
	}
	raw, err := json.Marshal(records)
	if err != nil {
		return ""
	}
	return string(raw)
}

func routerProviderAccountGroups(providers []provider.Registration, usages map[string]ProviderUsageSnapshot) []routerProviderAccountGroup {
	byKey := map[string]*routerProviderAccountGroup{}
	for _, registration := range providers {
		serviceKey := routerProviderServiceKey(registration)
		if serviceKey == "" {
			serviceKey = "provider"
		}
		service := routerCommandTitle(serviceKey)
		account := routerProviderAccountLabel(registration)
		if account == "" {
			account = registration.Identity.ProviderInstanceID
		}
		key := serviceKey + "\x00" + account
		group := byKey[key]
		if group == nil {
			group = &routerProviderAccountGroup{
				Service:    service,
				ServiceKey: serviceKey,
				Account:    account,
			}
			byKey[key] = group
		}
		group.Providers = append(group.Providers, registration)
		group.Nodes = appendStringUniqueRouter(group.Nodes, routerProviderNodeLabel(registration))
		group.ProviderIDs = appendStringUniqueRouter(group.ProviderIDs, registration.Identity.ProviderInstanceID)
		group.Health = appendStringUniqueRouter(group.Health, string(firstNonEmptyHealthStatus(registration.Health.Status)))
		group.Auth = appendStringUniqueRouter(group.Auth, string(firstNonEmptyAuthStatus(registration.Auth.Status)))
		group.Versions = appendStringUniqueRouter(group.Versions, registration.Identity.TargetVersion)
		group.Windows = append(group.Windows, routerProviderQuotaWindows(registration, usages[registration.Identity.ProviderInstanceID])...)
	}
	groups := make([]routerProviderAccountGroup, 0, len(byKey))
	for _, group := range byKey {
		sort.Strings(group.Nodes)
		sort.Strings(group.ProviderIDs)
		sort.Strings(group.Health)
		sort.Strings(group.Auth)
		sort.Strings(group.Versions)
		group.Windows = dedupeRouterQuotaWindows(group.Windows)
		sortRouterQuotaWindows(group.Windows)
		groups = append(groups, *group)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		switch {
		case groups[i].ServiceKey != groups[j].ServiceKey:
			return groups[i].ServiceKey < groups[j].ServiceKey
		case groups[i].Account != groups[j].Account:
			return groups[i].Account < groups[j].Account
		default:
			return strings.Join(groups[i].ProviderIDs, ",") < strings.Join(groups[j].ProviderIDs, ",")
		}
	})
	return groups
}

func renderRouterTelegramProviderAccountMessage(group routerProviderAccountGroup, seq int, deliveryType string, now time.Time) string {
	lines := routerProviderAccountBlockLines(group, seq, deliveryType, now, true)
	heading := "Pangaea Router · " + group.Service
	return "<b>" + html.EscapeString(heading) + "</b>\n<pre>" + html.EscapeString(strings.Join(lines, "\n")) + "</pre>"
}

func routerProviderAccountBlockLines(group routerProviderAccountGroup, seq int, deliveryType string, now time.Time, includeEventAt bool) []string {
	title := fmt.Sprintf("%s #%d", strings.ToLower(group.ServiceKey), seq)
	lines := []string{
		fmt.Sprintf("%s - %s", title, group.Account),
		"nodes: " + strings.Join(group.Nodes, ","),
	}
	if includeEventAt {
		lines = append([]string{lines[0], "event: " + deliveryType}, lines[1:]...)
	}
	if len(group.ProviderIDs) > 0 {
		lines = append(lines, "providers: "+strings.Join(group.ProviderIDs, ","))
	}
	if len(group.Versions) > 0 {
		lines = append(lines, "version: "+strings.Join(group.Versions, ","))
	}
	lines = append(lines,
		"health: "+strings.Join(group.Health, ","),
		"auth: "+strings.Join(group.Auth, ","),
	)
	if includeEventAt {
		lines = append(lines, "at: "+now.Local().Format("01-02 15:04:05"))
	}
	if len(group.Windows) == 0 {
		lines = append(lines, "", "quota not reported")
	} else {
		lines = append(lines, "", "quota:")
		for _, window := range group.Windows {
			lines = append(lines, routerQuotaWindowRows(window, now)...)
		}
	}
	return lines
}

func routerProviderServiceKey(registration provider.Registration) string {
	service := normalizeRouterCommandService(string(registration.Identity.Service))
	if service != "" {
		return service
	}
	return normalizeRouterCommandService(registration.Identity.ProviderType)
}

func firstNonEmptyHealthStatus(status provider.HealthStatus) provider.HealthStatus {
	if status != "" {
		return status
	}
	return provider.HealthUnknown
}

func firstNonEmptyAuthStatus(status provider.AuthStatus) provider.AuthStatus {
	if status != "" {
		return status
	}
	return provider.AuthUnknown
}

func appendStringUniqueRouter(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
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

func sortRouterQuotaWindows(windows []routerQuotaWindow) {
	sort.SliceStable(windows, func(i, j int) bool {
		if windows[i].Label != windows[j].Label {
			return windows[i].Label < windows[j].Label
		}
		switch {
		case windows[i].ResetAt.IsZero() && !windows[j].ResetAt.IsZero():
			return false
		case !windows[i].ResetAt.IsZero() && windows[j].ResetAt.IsZero():
			return true
		case !windows[i].ResetAt.Equal(windows[j].ResetAt):
			return windows[i].ResetAt.Before(windows[j].ResetAt)
		default:
			return routerQuotaRemainingPct(windows[i]) < routerQuotaRemainingPct(windows[j])
		}
	})
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

func sanitizeNotifierStatus(status NotifierStatus) NotifierStatus {
	status.Reason = scrubRouterNotifierSecret(status.Reason)
	status.LastError = scrubRouterNotifierSecret(status.LastError)
	return status
}

func sanitizeNotifierDelivery(delivery NotifierDelivery) NotifierDelivery {
	delivery.Message = scrubRouterNotifierSecret(delivery.Message)
	delivery.Error = scrubRouterNotifierSecret(delivery.Error)
	return delivery
}

func scrubRouterNotifierSecret(s string, secrets ...string) string {
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "<redacted>")
		}
	}
	return telegramBotTokenPattern.ReplaceAllStringFunc(s, func(match string) string {
		if strings.HasPrefix(match, "bot") {
			return "bot<redacted>"
		}
		return "<redacted>"
	})
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
