package grokauth

import (
	"context"
	"strings"
	"testing"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

func TestParseGrokAuthJSON(t *testing.T) {
	raw := []byte(`{"https://auth.x.ai::b1a00492-073a-47ea-816f-4c329264a828":{"key":"cached-token-value","email":"dev@example.test"}}`)
	snap, err := Format{}.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !strings.HasPrefix(snap.Identity(), "grok:") {
		t.Fatalf("identity = %q", snap.Identity())
	}
	result, err := Format{}.Validate(context.Background(), snap, formats.ValidateOpts{})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if result.Status != formats.StatusOK {
		t.Fatalf("status = %q", result.Status)
	}
	display, err := Format{}.AccountDisplay(context.Background(), snap, "")
	if err != nil {
		t.Fatalf("AccountDisplay: %v", err)
	}
	if display != "dev@example.test" {
		t.Fatalf("display = %q", display)
	}
	summary := Format{}.Redact(snap)
	if summary.TokenTail4 != "alue" || summary.FingerprintShort == "" {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestCredentialPathAcceptsHomeOrGrokDir(t *testing.T) {
	if got := (Format{}).CredentialPath("/home/me"); got != "/home/me/.grok/auth.json" {
		t.Fatalf("home path = %q", got)
	}
	if got := (Format{}).CredentialPath("/home/me/.grok"); got != "/home/me/.grok/auth.json" {
		t.Fatalf("grok dir path = %q", got)
	}
}
