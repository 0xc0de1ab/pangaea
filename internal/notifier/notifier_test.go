package notifier

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/internal/notifier/discord"
	"github.com/0xc0de1ab/pangaea/internal/notifier/mattermost"
	"github.com/0xc0de1ab/pangaea/internal/notifier/ntfy"
	"github.com/0xc0de1ab/pangaea/internal/notifier/slack"
	"github.com/0xc0de1ab/pangaea/internal/notifier/teams"
	"github.com/0xc0de1ab/pangaea/internal/notifier/telegram"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// fakeFormat implements formats.Format + UsageProbe for tests.
type fakeFormat struct {
	probe func(ctx context.Context, snap formats.Snapshot) (formats.UsageReport, error)
}

func (f fakeFormat) Name() string         { return "fake" }
func (f fakeFormat) Strategies() []string { return []string{"newer"} }
func (f fakeFormat) Parse(raw []byte) (formats.Snapshot, error) {
	return &fakeSnap{raw: raw}, nil
}
func (f fakeFormat) Validate(_ context.Context, _ formats.Snapshot, _ formats.ValidateOpts) (formats.ValidationResult, error) {
	return formats.ValidationResult{Status: formats.StatusOK}, nil
}
func (f fakeFormat) Compare(_ string, a, b formats.Snapshot) int { return 0 }
func (f fakeFormat) Redact(_ formats.Snapshot) formats.Summary   { return formats.Summary{} }
func (f fakeFormat) Probe(ctx context.Context, snap formats.Snapshot, _ string, _ *http.Client) (formats.UsageReport, error) {
	if f.probe == nil {
		return formats.UsageReport{}, nil
	}
	return f.probe(ctx, snap)
}

type fakeSnap struct{ raw []byte }

func (s *fakeSnap) Identity() string     { return "id" }
func (s *fakeSnap) ExpiresAt() time.Time { return time.Time{} }
func (s *fakeSnap) Raw() []byte          { return s.raw }
func (s *fakeSnap) Fingerprint() string  { return "fpfpfpfpfpfpfpfpfpfpfpfpfpfpfpfp" }

func mustSummaryRaw(t *testing.T, exp time.Time, subscription string, extra map[string]string) []byte {
	t.Helper()
	raw, err := json.Marshal(formats.Summary{
		Identity:         "acct@example.test",
		Subscription:     subscription,
		FingerprintShort: "abc123def456",
		ExpiresAt:        exp,
		Extra:            extra,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestTelegramSink_RoutesPerAccount(t *testing.T) {
	var mu sync.Mutex
	var sentTo []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChatID string `json:"chat_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		sentTo = append(sentTo, body.ChatID)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	tg := &telegram.Client{BotToken: "T", Endpoint: srv.URL, HTTP: srv.Client()}
	sink := NewTelegramSink(TelegramSinkConfig{
		Routes: []TelegramRoute{
			{Profile: "p1", Account: "user1", ChatID: "chat-1"},
			{Profile: "p1", Account: "user2", ChatID: "chat-2"},
		},
	}, tg)

	rec := []TruthRecord{
		{Profile: "p1", Account: "user1", Format: "fake", RawB64: base64.StdEncoding.EncodeToString([]byte(`{}`))},
		{Profile: "p1", Account: "user2", Format: "fake", RawB64: base64.StdEncoding.EncodeToString([]byte(`{}`))},
	}
	src := func(_ context.Context) []TruthRecord { return rec }
	formatsLookup := func(_ string) (formats.Format, bool) { return fakeFormat{}, true }
	cfg := Config{Interval: 100 * time.Millisecond}
	n := New(cfg, []Sink{sink}, src, formatsLookup, srv.Client(), logging.New(logging.Options{Level: "error"}))
	n.runOnce(context.Background())

	if len(sentTo) != 2 {
		t.Fatalf("sent %d, want 2", len(sentTo))
	}
	want := map[string]bool{"chat-1": true, "chat-2": true}
	for _, c := range sentTo {
		if !want[c] {
			t.Fatalf("unexpected chat %q", c)
		}
	}
}

func TestSlackSink_RoutesPerAccount(t *testing.T) {
	var mu sync.Mutex
	var seenURLs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenURLs = append(seenURLs, r.URL.Path)
		mu.Unlock()
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	urlA := srv.URL + "/services/T/B/AAA"
	urlB := srv.URL + "/services/T/B/BBB"

	sink := NewSlackSink(SlackSinkConfig{
		Routes: []SlackRoute{
			{Profile: "p1", Account: "user1", WebhookURL: urlA},
			{Profile: "p1", Account: "user2", WebhookURL: urlB},
		},
	}, &slack.Client{HTTP: srv.Client()})

	rec := []TruthRecord{
		{Profile: "p1", Account: "user1", Format: "fake", RawB64: base64.StdEncoding.EncodeToString([]byte(`{}`))},
		{Profile: "p1", Account: "user2", Format: "fake", RawB64: base64.StdEncoding.EncodeToString([]byte(`{}`))},
	}
	cfg := Config{Interval: 100 * time.Millisecond}
	n := New(cfg, []Sink{sink}, func(_ context.Context) []TruthRecord { return rec },
		func(_ string) (formats.Format, bool) { return fakeFormat{}, true },
		srv.Client(), logging.New(logging.Options{Level: "error"}))
	n.runOnce(context.Background())

	if len(seenURLs) != 2 {
		t.Fatalf("got %d, want 2; %v", len(seenURLs), seenURLs)
	}
	want := map[string]bool{"/services/T/B/AAA": true, "/services/T/B/BBB": true}
	for _, p := range seenURLs {
		if !want[p] {
			t.Fatalf("unexpected URL path %q", p)
		}
	}
}

func TestNotifier_FansOutToBothSinks(t *testing.T) {
	var tgCalls, slCalls int32
	tgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&tgCalls, 1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tgSrv.Close()
	slSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&slCalls, 1)
		_, _ = w.Write([]byte("ok"))
	}))
	defer slSrv.Close()

	tgSink := NewTelegramSink(TelegramSinkConfig{DefaultChatID: "C"},
		&telegram.Client{BotToken: "T", Endpoint: tgSrv.URL, HTTP: tgSrv.Client()})
	slSink := NewSlackSink(SlackSinkConfig{DefaultWebhookURL: slSrv.URL + "/x"},
		&slack.Client{HTTP: slSrv.Client()})

	rec := []TruthRecord{{Profile: "p1", Account: "a1", Format: "fake", RawB64: base64.StdEncoding.EncodeToString([]byte(`{}`))}}
	n := New(Config{Interval: time.Second}, []Sink{tgSink, slSink},
		func(_ context.Context) []TruthRecord { return rec },
		func(_ string) (formats.Format, bool) { return fakeFormat{}, true },
		tgSrv.Client(), logging.New(logging.Options{Level: "error"}))
	n.runOnce(context.Background())

	if atomic.LoadInt32(&tgCalls) != 1 {
		t.Fatalf("telegram calls = %d", tgCalls)
	}
	if atomic.LoadInt32(&slCalls) != 1 {
		t.Fatalf("slack calls = %d", slCalls)
	}
}

func TestNotifier_NoRouteSkippedSilently(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	tg := &telegram.Client{BotToken: "T", Endpoint: srv.URL, HTTP: srv.Client()}
	sink := NewTelegramSink(TelegramSinkConfig{}, tg) // no routes, no default
	rec := []TruthRecord{{Profile: "p", Account: "a", Format: "fake", RawB64: base64.StdEncoding.EncodeToString([]byte(`{}`))}}
	n := New(Config{Interval: time.Second}, []Sink{sink},
		func(_ context.Context) []TruthRecord { return rec },
		func(_ string) (formats.Format, bool) { return fakeFormat{}, true },
		srv.Client(), logging.New(logging.Options{Level: "error"}))
	n.runOnce(context.Background())
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("expected 0 calls (no route), got %d", got)
	}
}

func TestNotifier_UsageProbeIncludedInBothMessages(t *testing.T) {
	var tgBody string
	var slBody string
	tgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		tgBody = b.Text
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tgSrv.Close()
	slSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		slBody = b.Text
		_, _ = w.Write([]byte("ok"))
	}))
	defer slSrv.Close()

	probedFormat := fakeFormat{probe: func(_ context.Context, _ formats.Snapshot) (formats.UsageReport, error) {
		return formats.UsageReport{
			PlanTier: "max-20x",
			Windows: []formats.UsageWindow{{
				Label:        "Current session",
				RemainingPct: 87.7,
				ResetAt:      time.Date(2026, 4, 26, 12, 30, 0, 0, time.UTC),
			}},
			Notes: []string{"org: Acme Inc"},
		}, nil
	}}
	tgSink := NewTelegramSink(TelegramSinkConfig{DefaultChatID: "C"},
		&telegram.Client{BotToken: "T", Endpoint: tgSrv.URL, HTTP: tgSrv.Client()})
	slSink := NewSlackSink(SlackSinkConfig{DefaultWebhookURL: slSrv.URL + "/x"},
		&slack.Client{HTTP: slSrv.Client()})

	rec := TruthRecord{
		Profile:     "claude-prod",
		Account:     "acct@example.test",
		Format:      "claude-credentials-json-format",
		RawB64:      base64.StdEncoding.EncodeToString([]byte(`{}`)),
		Summary:     mustSummaryRaw(t, time.Now().Add(2*time.Hour), "max", map[string]string{"email": "acct@example.test"}),
		PrevSummary: mustSummaryRaw(t, time.Now().Add(30*time.Minute), "max", map[string]string{"email": "acct@example.test"}),
		SourceNode:  "node-a",
		TargetNodes: []string{"node-b", "node-c"},
	}
	n := New(Config{Interval: time.Second}, []Sink{tgSink, slSink},
		func(_ context.Context) []TruthRecord { return nil },
		func(_ string) (formats.Format, bool) { return probedFormat, true },
		tgSrv.Client(), logging.New(logging.Options{Level: "error"}))
	n.Emit(context.Background(), rec)

	for _, want := range []string{"Auth Propagated", "Claude", "node-a", "node-b", "max-20x", "Acme Inc"} {
		if !strings.Contains(tgBody, want) {
			t.Fatalf("telegram missing %q\n--- text ---\n%s", want, tgBody)
		}
		if !strings.Contains(slBody, want) {
			t.Fatalf("slack missing %q\n--- text ---\n%s", want, slBody)
		}
	}
	for _, want := range []string{"<pre>", "Expiry Change", "Latest Detected", "Propagated To", "Current session", "█"} {
		if !strings.Contains(tgBody, want) {
			t.Fatalf("telegram missing %q\n--- text ---\n%s", want, tgBody)
		}
	}
	for _, want := range []string{"expiry change", "Current session", "88% left"} {
		if !strings.Contains(slBody, want) {
			t.Fatalf("slack missing %q\n--- text ---\n%s", want, slBody)
		}
	}
	// Markup difference assertions.
	if !strings.Contains(tgBody, "<b>Auth Propagated</b>") {
		t.Fatalf("telegram should use HTML heading markup: %s", tgBody)
	}
	if !strings.Contains(slBody, "*Auth Propagated*") {
		t.Fatalf("slack should use mrkdwn *bold*: %s", slBody)
	}
}

func TestNotifier_AllSixSinksFanOut(t *testing.T) {
	type seen struct {
		sink string
		text string
	}
	var mu sync.Mutex
	var hits []seen

	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		hits = append(hits, seen{"telegram", b.Text})
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tg.Close()
	sl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		hits = append(hits, seen{"slack", b.Text})
		mu.Unlock()
		_, _ = w.Write([]byte("ok"))
	}))
	defer sl.Close()
	dc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		hits = append(hits, seen{"discord", b.Content})
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer dc.Close()
	mm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		hits = append(hits, seen{"mattermost", b.Text})
		mu.Unlock()
		_, _ = w.Write([]byte("ok"))
	}))
	defer mm.Close()
	nf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		hits = append(hits, seen{"ntfy", string(body)})
		mu.Unlock()
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer nf.Close()
	tm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var card struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&card)
		mu.Lock()
		hits = append(hits, seen{"teams", card.Text})
		mu.Unlock()
		_, _ = w.Write([]byte("1"))
	}))
	defer tm.Close()

	sinks := []Sink{
		NewTelegramSink(TelegramSinkConfig{DefaultChatID: "C"},
			&telegram.Client{BotToken: "T", Endpoint: tg.URL, HTTP: tg.Client()}),
		NewSlackSink(SlackSinkConfig{DefaultWebhookURL: sl.URL + "/x"},
			&slack.Client{HTTP: sl.Client()}),
		NewDiscordSink(DiscordSinkConfig{DefaultWebhookURL: dc.URL + "/x"},
			&discord.Client{HTTP: dc.Client()}),
		NewMattermostSink(MattermostSinkConfig{DefaultWebhookURL: mm.URL + "/x"},
			&mattermost.Client{HTTP: mm.Client()}),
		NewNtfySink(NtfySinkConfig{DefaultTopicURL: nf.URL + "/topic1"},
			&ntfy.Client{HTTP: nf.Client()}),
		NewTeamsSink(TeamsSinkConfig{DefaultWebhookURL: tm.URL + "/x"},
			&teams.Client{HTTP: tm.Client()}),
	}

	rec := []TruthRecord{{
		Profile:     "claude-prod",
		Account:     "u1@example.test",
		Format:      "claude-credentials-json-format",
		RawB64:      base64.StdEncoding.EncodeToString([]byte(`{}`)),
		Summary:     mustSummaryRaw(t, time.Now().Add(3*time.Hour), "max", map[string]string{"email": "u1@example.test"}),
		SourceNode:  "node-a",
		TargetNodes: []string{"node-b", "node-c"},
	}}
	probedFormat := fakeFormat{probe: func(_ context.Context, _ formats.Snapshot) (formats.UsageReport, error) {
		return formats.UsageReport{PlanTier: "max-20x", Used: 100, Limit: 1000, Unit: "msgs"}, nil
	}}
	n := New(Config{Interval: time.Second}, sinks,
		func(_ context.Context) []TruthRecord { return nil },
		func(_ string) (formats.Format, bool) { return probedFormat, true },
		tg.Client(), logging.New(logging.Options{Level: "error"}))
	n.Emit(context.Background(), rec[0])

	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 6 {
		t.Fatalf("expected one hit per sink (6), got %d: %+v", len(hits), hits)
	}
	gotSinks := map[string]bool{}
	for _, h := range hits {
		gotSinks[h.sink] = true
		for _, want := range []string{"Auth Propagated", "Claude", "node-a", "node-b", "valid until", "max-20x"} {
			if h.sink == "telegram" && want == "valid until" {
				continue
			}
			if !strings.Contains(h.text, want) {
				t.Fatalf("sink %s missing %q in body: %q", h.sink, want, h.text)
			}
		}
		if h.sink == "telegram" {
			for _, want := range []string{"<pre>", "Valid Until"} {
				if !strings.Contains(h.text, want) {
					t.Fatalf("sink %s missing %q in body: %q", h.sink, want, h.text)
				}
			}
		}
		if !strings.Contains(h.text, "100") || !strings.Contains(h.text, "1000") {
			t.Fatalf("sink %s missing usage numbers: %q", h.sink, h.text)
		}
		if h.sink == "telegram" {
			if !strings.Contains(h.text, "Fingerprint") {
				t.Fatalf("sink %s missing fingerprint label: %q", h.sink, h.text)
			}
		} else if !strings.Contains(h.text, "fingerprint") {
			t.Fatalf("sink %s missing fingerprint label: %q", h.sink, h.text)
		}
		if !strings.Contains(h.text, "max-20x") {
			t.Fatalf("sink %s missing plan tier in body: %q", h.sink, h.text)
		}
	}
	for _, s := range []string{"telegram", "slack", "discord", "mattermost", "ntfy", "teams"} {
		if !gotSinks[s] {
			t.Fatalf("sink %s did not receive a message", s)
		}
	}
	// Markup variants — each renderer's signature.
	for _, h := range hits {
		switch h.sink {
		case "telegram":
			if !strings.Contains(h.text, "<b>Auth Propagated</b>") {
				t.Fatalf("telegram should use HTML <b>: %s", h.text)
			}
		case "slack", "mattermost":
			if !strings.Contains(h.text, "*Auth Propagated*") {
				t.Fatalf("%s should use mrkdwn *bold*: %s", h.sink, h.text)
			}
		case "discord":
			if !strings.Contains(h.text, "**Auth Propagated**") {
				t.Fatalf("discord should use **bold**: %s", h.text)
			}
		case "teams":
			if !strings.Contains(h.text, "**Auth Propagated**") {
				t.Fatalf("teams should use **bold** labels: %s", h.text)
			}
		case "ntfy":
			if strings.Contains(h.text, "**Auth Propagated**") || strings.Contains(h.text, "<b>Auth Propagated</b>") {
				t.Fatalf("ntfy should be plain text: %s", h.text)
			}
		}
	}
}

func TestNotifier_PeriodicSummaryAggregatesAndSkipsUnchanged(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b.Text)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	oldNow := nowFunc
	currentNow := time.Date(2026, 4, 26, 9, 0, 0, 0, time.FixedZone("KST", 9*60*60))
	nowFunc = func() time.Time { return currentNow }
	defer func() { nowFunc = oldNow }()

	sink := NewTelegramSink(TelegramSinkConfig{DefaultChatID: "C"},
		&telegram.Client{BotToken: "T", Endpoint: srv.URL, HTTP: srv.Client()})
	rec := []TruthRecord{
		{
			Profile: "claude",
			Account: "acct-1",
			Format:  "claude-credentials-json-format",
			RawB64:  base64.StdEncoding.EncodeToString([]byte(`{}`)),
			Summary: mustSummaryRaw(t, time.Date(2026, 4, 30, 14, 31, 0, 0, time.UTC), "max", map[string]string{
				"display_account": "acct@example.test",
				"last_refresh":    "2026-04-26T00:12:13Z",
			}),
			Nodes: []string{"snowbox", "opi5"},
		},
		{
			Profile: "codex",
			Account: "acct-2",
			Format:  "codex-auth-json-format",
			RawB64:  base64.StdEncoding.EncodeToString([]byte(`{}`)),
			Summary: mustSummaryRaw(t, time.Date(2026, 4, 28, 11, 0, 0, 0, time.UTC), "pro", map[string]string{
				"account_id":      "user-123",
				"display_account": "codex@example.test",
				"last_refresh":    "2026-04-26T01:02:03Z",
			}),
			Nodes: []string{"snowbox"},
		},
		{
			Profile: "gemini",
			Format:  "gemini-oauth-creds-json-format",
			NoTruth: true,
			Nodes:   []string{"opi5", "snowbox"},
		},
	}
	n := New(Config{Interval: time.Minute}, []Sink{sink},
		func(_ context.Context) []TruthRecord { return rec },
		func(_ string) (formats.Format, bool) {
			return fakeFormat{probe: func(_ context.Context, _ formats.Snapshot) (formats.UsageReport, error) {
				return formats.UsageReport{
					Windows: []formats.UsageWindow{{
						Label:        "Current session",
						RemainingPct: 93,
						ResetAt:      time.Date(2026, 4, 26, 3, 30, 0, 0, time.UTC),
					}},
				}, nil
			}}, true
		},
		srv.Client(), logging.New(logging.Options{Level: "error"}))

	n.runOnce(context.Background())
	currentNow = currentNow.Add(time.Hour)
	n.runOnce(context.Background())

	if len(bodies) != 1 {
		t.Fatalf("expected exactly one periodic send after dedupe, got %d", len(bodies))
	}
	body := bodies[0]
	for _, want := range []string{
		"<b>Auth State</b>",
		"<b>claude #1</b>",
		"opi5,snowbox",
		"<b>codex #1</b>",
		"Current session",
		"2026-04-30 23:31:00.000",
		"acct@example.test",
		"last refresh",
		"2026-04-26 10:02:03.000",
		"<b>gemini #1</b> - ❌ no truth",
		"█",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("periodic body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

func TestNotifier_ProbeFailureDoesNotBlockSend(t *testing.T) {
	var sent int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&sent, 1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	tg := &telegram.Client{BotToken: "T", Endpoint: srv.URL, HTTP: srv.Client()}
	probedFormat := fakeFormat{probe: func(_ context.Context, _ formats.Snapshot) (formats.UsageReport, error) {
		return formats.UsageReport{}, http.ErrHandlerTimeout
	}}
	sink := NewTelegramSink(TelegramSinkConfig{DefaultChatID: "C"}, tg)
	rec := []TruthRecord{{Profile: "p", Account: "a", Format: "fake", RawB64: base64.StdEncoding.EncodeToString([]byte(`{}`))}}
	n := New(Config{Interval: time.Second}, []Sink{sink},
		func(_ context.Context) []TruthRecord { return rec },
		func(_ string) (formats.Format, bool) { return probedFormat, true },
		srv.Client(), logging.New(logging.Options{Level: "error"}))
	n.runOnce(context.Background())
	if got := atomic.LoadInt32(&sent); got != 1 {
		t.Fatalf("expected 1 call, got %d", got)
	}
}

func TestNotifier_EmitSendsImmediately(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		body = payload.Text
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	sink := NewTelegramSink(TelegramSinkConfig{DefaultChatID: "C"},
		&telegram.Client{BotToken: "T", Endpoint: srv.URL, HTTP: srv.Client()})
	probedFormat := fakeFormat{probe: func(_ context.Context, _ formats.Snapshot) (formats.UsageReport, error) {
		return formats.UsageReport{Windows: []formats.UsageWindow{{
			Label:        "Current session",
			RemainingPct: 88,
		}}}, nil
	}}
	n := New(Config{Interval: time.Hour}, []Sink{sink},
		func(_ context.Context) []TruthRecord { return nil },
		func(_ string) (formats.Format, bool) { return probedFormat, true },
		srv.Client(), logging.New(logging.Options{Level: "error"}))

	n.Emit(context.Background(), TruthRecord{
		Profile:     "claude-prod",
		Account:     "acct@example.test",
		Format:      "claude-credentials-json-format",
		RawB64:      base64.StdEncoding.EncodeToString([]byte(`{}`)),
		Summary:     mustSummaryRaw(t, time.Now().Add(time.Hour), "max", nil),
		SourceNode:  "node-a",
		TargetNodes: []string{"node-b"},
	})

	for _, want := range []string{"Auth Propagated", "node-a", "node-b"} {
		if !strings.Contains(body, want) {
			t.Fatalf("message missing %q: %s", want, body)
		}
	}
}

func TestNotifier_PropagationWithoutUsageIsSuppressed(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	sink := NewTelegramSink(TelegramSinkConfig{DefaultChatID: "C"},
		&telegram.Client{BotToken: "T", Endpoint: srv.URL, HTTP: srv.Client()})
	n := New(Config{Interval: time.Hour}, []Sink{sink},
		func(_ context.Context) []TruthRecord { return nil },
		func(_ string) (formats.Format, bool) { return fakeFormat{}, true },
		srv.Client(), logging.New(logging.Options{Level: "error"}))

	n.Emit(context.Background(), TruthRecord{
		Profile:     "claude-prod",
		Account:     "acct@example.test",
		Format:      "fake",
		RawB64:      base64.StdEncoding.EncodeToString([]byte(`{}`)),
		Summary:     mustSummaryRaw(t, time.Now().Add(time.Hour), "max", nil),
		SourceNode:  "node-a",
		TargetNodes: []string{"node-b"},
	})

	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("propagation without usage should be suppressed, got %d sends", got)
	}
}

func TestNotifier_TruthRestoredUsesHeadingAndUsageProbe(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		body = payload.Text
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	sink := NewTelegramSink(TelegramSinkConfig{DefaultChatID: "C"},
		&telegram.Client{BotToken: "T", Endpoint: srv.URL, HTTP: srv.Client()})
	probedFormat := fakeFormat{probe: func(_ context.Context, _ formats.Snapshot) (formats.UsageReport, error) {
		return formats.UsageReport{
			PlanTier: "max",
			Windows: []formats.UsageWindow{{
				Label:        "Current session",
				RemainingPct: 88,
				ResetAt:      time.Date(2026, 4, 26, 12, 30, 0, 0, time.UTC),
			}},
		}, nil
	}}
	n := New(Config{Interval: time.Hour}, []Sink{sink},
		func(_ context.Context) []TruthRecord { return nil },
		func(_ string) (formats.Format, bool) { return probedFormat, true },
		srv.Client(), logging.New(logging.Options{Level: "error"}))

	n.Emit(context.Background(), TruthRecord{
		Profile:     "gemini",
		Account:     "acct-1",
		Format:      "fake",
		Fingerprint: "fp-1",
		RawB64:      base64.StdEncoding.EncodeToString([]byte(`{}`)),
		Summary:     mustSummaryRaw(t, time.Now().Add(time.Hour), "max", map[string]string{"account_id": "acct-1"}),
		SourceNode:  "snowbox",
		Nodes:       []string{"snowbox"},
		EventKind:   EventTruthRestored,
	})

	for _, want := range []string{"Truth Restored", "snowbox", "Current session", "88% left"} {
		if !strings.Contains(body, want) {
			t.Fatalf("message missing %q: %s", want, body)
		}
	}
}

func TestNotifier_StartupGraceSuppressesTruthEvents(t *testing.T) {
	oldNow := nowFunc
	currentNow := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return currentNow }
	defer func() { nowFunc = oldNow }()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	sink := NewTelegramSink(TelegramSinkConfig{DefaultChatID: "C"},
		&telegram.Client{BotToken: "T", Endpoint: srv.URL, HTTP: srv.Client()})
	probedFormat := fakeFormat{probe: func(_ context.Context, _ formats.Snapshot) (formats.UsageReport, error) {
		return formats.UsageReport{Windows: []formats.UsageWindow{{Label: "Current session", RemainingPct: 88}}}, nil
	}}
	n := New(Config{Interval: time.Hour, StartupEventGrace: time.Minute}, []Sink{sink},
		func(_ context.Context) []TruthRecord { return nil },
		func(_ string) (formats.Format, bool) { return probedFormat, true },
		srv.Client(), logging.New(logging.Options{Level: "error"}))

	rec := TruthRecord{
		Profile:     "gemini",
		Account:     "acct-1",
		Format:      "fake",
		Fingerprint: "fp-1",
		RawB64:      base64.StdEncoding.EncodeToString([]byte(`{}`)),
		Summary:     mustSummaryRaw(t, time.Now().Add(time.Hour), "max", nil),
		SourceNode:  "snowbox",
		Nodes:       []string{"snowbox"},
		EventKind:   EventTruthRestored,
	}
	n.Emit(context.Background(), rec)
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("startup truth event should be suppressed, got %d sends", got)
	}

	currentNow = currentNow.Add(time.Minute + time.Second)
	n.Emit(context.Background(), rec)
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("truth event after startup grace should send once, got %d", got)
	}
}

func TestNotifier_RunOnceSkipsBlankStartupState(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	sink := NewTelegramSink(TelegramSinkConfig{DefaultChatID: "C"},
		&telegram.Client{BotToken: "T", Endpoint: srv.URL, HTTP: srv.Client()})
	n := New(Config{Interval: time.Hour}, []Sink{sink},
		func(_ context.Context) []TruthRecord {
			return []TruthRecord{
				{Profile: "claude", Format: "claude-credentials-json-format", NoTruth: true},
				{Profile: "codex", Format: "codex-auth-json-format", NoTruth: true},
				{Profile: "gemini", Format: "gemini-oauth-creds-json-format", NoTruth: true},
			}
		},
		func(_ string) (formats.Format, bool) { return fakeFormat{}, true },
		srv.Client(), logging.New(logging.Options{Level: "error"}))

	if ready := n.runOnce(context.Background()); ready {
		t.Fatalf("blank startup state should not be considered ready")
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("expected no sends for blank startup state, got %d", got)
	}
}

func TestNotifier_HidesOpaqueAccountIDsInMessages(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		body = payload.Text
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	sink := NewTelegramSink(TelegramSinkConfig{DefaultChatID: "C"},
		&telegram.Client{BotToken: "T", Endpoint: srv.URL, HTTP: srv.Client()})
	n := New(Config{Interval: time.Hour}, []Sink{sink},
		func(_ context.Context) []TruthRecord { return nil },
		func(_ string) (formats.Format, bool) { return fakeFormat{}, true },
		srv.Client(), logging.New(logging.Options{Level: "error"}))

	const testAccountID = "11111111-2222-3333-4444-555555555555"

	n.Emit(context.Background(), TruthRecord{
		Profile:     "claude",
		Account:     testAccountID,
		Format:      "claude-credentials-json-format",
		RawB64:      base64.StdEncoding.EncodeToString([]byte(`{}`)),
		Summary:     mustSummaryRaw(t, time.Now().Add(time.Hour), "max", map[string]string{"account_id": testAccountID}),
		SourceNode:  "snowbox",
		TargetNodes: []string{"opi5"},
	})

	if strings.Contains(body, testAccountID) {
		t.Fatalf("opaque account id should be hidden: %s", body)
	}
	if strings.Contains(body, "11111111") || strings.Contains(body, "5555") {
		t.Fatalf("opaque account id fragments should be hidden: %s", body)
	}
}

func TestTelegramPeriodic_ZeroRemainingPctRendersProgress(t *testing.T) {
	oldNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 4, 29, 0, 5, 0, 0, seoulTZ) }
	defer func() { nowFunc = oldNow }()

	body := renderPeriodicTelegram([]ReportRecord{{
		Truth: TruthRecord{
			Profile: "claude",
			Format:  "claude-credentials-json-format",
			Summary: mustSummaryRaw(t, time.Date(2026, 4, 29, 2, 0, 0, 0, time.UTC), "max", map[string]string{
				"display_account": "claude@example.test",
			}),
		},
		Usage: formats.UsageReport{Windows: []formats.UsageWindow{{
			Label:        "Current session",
			RemainingPct: 0,
			ResetAt:      time.Date(2026, 4, 28, 16, 30, 0, 0, time.UTC),
		}}},
	}})

	for _, want := range []string{
		"Current session",
		"░░░░░░░░░░░░",
		"0% left",
		"2026-04-29 01:30:00.000",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

func TestNotifier_SuppressesSessionEvents(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		body = payload.Text
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	sink := NewTelegramSink(TelegramSinkConfig{DefaultChatID: "C"},
		&telegram.Client{BotToken: "T", Endpoint: srv.URL, HTTP: srv.Client()})
	n := New(Config{Interval: time.Hour}, []Sink{sink},
		func(_ context.Context) []TruthRecord { return nil },
		func(_ string) (formats.Format, bool) { return fakeFormat{}, true },
		srv.Client(), logging.New(logging.Options{Level: "error"}))

	n.Emit(context.Background(), TruthRecord{
		Profile:   "claude",
		Format:    "claude-credentials-json-format",
		EventKind: EventSessionConnected,
		NodeID:    "opi5(server)",
		PeerCN:    "opi5(server)",
		AuthMode:  "mtls",
		Nodes:     []string{"opi5(server)", "snowbox"},
	})

	time.Sleep(50 * time.Millisecond)
	if body != "" {
		t.Fatalf("session events should not notify sinks, got: %s", body)
	}
}
