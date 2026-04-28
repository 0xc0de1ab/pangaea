package notifier

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/internal/notifier/telegram"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

func TestTelegramCommandPoller_ProfileCommand(t *testing.T) {
	oldWait := telegramCommandRefreshWait
	telegramCommandRefreshWait = time.Millisecond
	t.Cleanup(func() { telegramCommandRefreshWait = oldWait })

	var sent telegram.SendMessageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			_ = json.NewDecoder(r.Body).Decode(&sent)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	refreshed := ""
	p := NewTelegramCommandPoller(
		TelegramCommandConfig{DefaultChatID: "100"},
		&telegram.Client{BotToken: "T", Endpoint: srv.URL, HTTP: srv.Client()},
		func(context.Context) []TruthRecord {
			return []TruthRecord{{
				Profile: "gemini",
				Format:  "gemini-oauth-creds-json-format",
				Account: "acct",
				Summary: mustSummaryRaw(t, time.Now().Add(time.Hour), "", map[string]string{
					"account_id":      "103346917865536688609",
					"display_account": "gemini@example.test",
				}),
				Nodes: []string{"a2"},
			}}
		},
		func(string) (formats.Format, bool) { return fakeFormat{}, true },
		srv.Client(),
		func(_ context.Context, profile string) (int, error) {
			refreshed = profile
			return 1, nil
		},
		logging.New(logging.Options{Level: "error"}),
	)

	p.handleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			MessageID: 7,
			Chat:      telegram.Chat{ID: 100},
			Text:      "/gemini",
		},
	})

	if refreshed != "gemini" {
		t.Fatalf("refresh profile = %q", refreshed)
	}
	if sent.ChatID != "100" || sent.ReplyToMessageID != 7 {
		t.Fatalf("sent = %+v", sent)
	}
	for _, want := range []string{"Auth State", "gemini #1", "a2", "gemini@example.test"} {
		if !strings.Contains(sent.Text, want) {
			t.Fatalf("response missing %q: %s", want, sent.Text)
		}
	}
}

func TestTelegramCommandPoller_InvalidProfile(t *testing.T) {
	var sent telegram.SendMessageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	p := NewTelegramCommandPoller(
		TelegramCommandConfig{DefaultChatID: "100"},
		&telegram.Client{BotToken: "T", Endpoint: srv.URL, HTTP: srv.Client()},
		func(context.Context) []TruthRecord {
			return []TruthRecord{{Profile: "claude"}, {Profile: "codex"}, {Profile: "gemini"}}
		},
		func(string) (formats.Format, bool) { return fakeFormat{}, true },
		srv.Client(),
		nil,
		logging.New(logging.Options{Level: "error"}),
	)

	p.handleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			MessageID: 7,
			Chat:      telegram.Chat{ID: 100},
			Text:      "/wat",
		},
	})

	if !strings.Contains(sent.Text, "Unknown profile") || !strings.Contains(sent.Text, "claude, codex, gemini") {
		t.Fatalf("unexpected error response: %s", sent.Text)
	}
}
