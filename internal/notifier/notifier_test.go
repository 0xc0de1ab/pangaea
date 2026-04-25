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
			PlanTier: "max-20x", Used: 1234, Limit: 10000, Unit: "messages",
			Notes: []string{"org: Acme Inc"},
		}, nil
	}}
	tgSink := NewTelegramSink(TelegramSinkConfig{DefaultChatID: "C"},
		&telegram.Client{BotToken: "T", Endpoint: tgSrv.URL, HTTP: tgSrv.Client()})
	slSink := NewSlackSink(SlackSinkConfig{DefaultWebhookURL: slSrv.URL + "/x"},
		&slack.Client{HTTP: slSrv.Client()})

	rec := []TruthRecord{{
		Profile:     "claude-prod",
		Account:     "acct@example.test",
		Format:      "claude-credentials-json-format",
		RawB64:      base64.StdEncoding.EncodeToString([]byte(`{}`)),
		Summary:     mustSummaryRaw(t, time.Now().Add(2*time.Hour), "max", map[string]string{"email": "acct@example.test"}),
		SourceNode:  "node-a",
		TargetNodes: []string{"node-b", "node-c"},
	}}
	n := New(Config{Interval: time.Second}, []Sink{tgSink, slSink},
		func(_ context.Context) []TruthRecord { return rec },
		func(_ string) (formats.Format, bool) { return probedFormat, true },
		tgSrv.Client(), logging.New(logging.Options{Level: "error"}))
	n.runOnce(context.Background())

	for _, want := range []string{"Auth Propagated", "Claude", "node-a", "node-b", "valid until", "max-20x", "1234", "10000", "Acme Inc"} {
		if !strings.Contains(tgBody, want) {
			t.Fatalf("telegram missing %q\n--- text ---\n%s", want, tgBody)
		}
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
		func(_ context.Context) []TruthRecord { return rec },
		func(_ string) (formats.Format, bool) { return probedFormat, true },
		tg.Client(), logging.New(logging.Options{Level: "error"}))
	n.runOnce(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 6 {
		t.Fatalf("expected one hit per sink (6), got %d: %+v", len(hits), hits)
	}
	gotSinks := map[string]bool{}
	for _, h := range hits {
		gotSinks[h.sink] = true
		for _, want := range []string{"Auth Propagated", "Claude", "node-a", "node-b", "valid until", "max-20x"} {
			if !strings.Contains(h.text, want) {
				t.Fatalf("sink %s missing %q in body: %q", h.sink, want, h.text)
			}
		}
		if !strings.Contains(h.text, "100") || !strings.Contains(h.text, "1000") {
			t.Fatalf("sink %s missing usage numbers: %q", h.sink, h.text)
		}
		if !strings.Contains(h.text, "fingerprint") {
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
			if strings.Contains(h.text, "**") || strings.Contains(h.text, "<b>") {
				t.Fatalf("ntfy should be plain text: %s", h.text)
			}
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
	n := New(Config{Interval: time.Hour}, []Sink{sink},
		func(_ context.Context) []TruthRecord { return nil },
		func(_ string) (formats.Format, bool) { return fakeFormat{}, true },
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
