package common

import (
	"errors"
	"strings"
	"testing"
)

// Server and client configs are loaded with distinct viper instances, so the
// Key namespaces don't collide at runtime. We enforce uniqueness per group.
func TestKeysUnique(t *testing.T) {
	groups := map[string]func(name string) bool{
		"server": func(n string) bool { return strings.HasPrefix(n, "KeyServer") },
		"client": func(n string) bool { return strings.HasPrefix(n, "KeyClient") },
		"flag":   func(n string) bool { return strings.HasPrefix(n, "Flag") },
	}
	all := listStringConsts()
	for group, match := range groups {
		seen := map[string]string{}
		for name, val := range all {
			if !match(name) {
				continue
			}
			if prev, ok := seen[val]; ok {
				t.Fatalf("[%s] duplicate value %q: %s and %s", group, val, prev, name)
			}
			seen[val] = name
		}
	}
}

// listStringConsts enumerates the set of (name,value) pairs we want uniqueness
// for. Go reflect cannot list package-level constants directly, so we mirror
// them manually — adding a new Key*/Flag* constant requires a one-line edit
// here. The test then guarantees no two share the same string value.
func listStringConsts() map[string]string {
	return map[string]string{
		"KeyServerListen":         KeyServerListen,
		"KeyServerPKICA":          KeyServerPKICA,
		"KeyServerPKICert":        KeyServerPKICert,
		"KeyServerPKIKey":         KeyServerPKIKey,
		"KeyServerLogLevel":       KeyServerLogLevel,
		"KeyServerLogFormat":      KeyServerLogFormat,
		"KeyServerProfilesFile":   KeyServerProfilesFile,
		"KeyServerSelfNodeEnable": KeyServerSelfNodeEnable,
		"KeyServerSelfNodeCert":   KeyServerSelfNodeCert,
		"KeyServerSelfNodeKey":    KeyServerSelfNodeKey,
		"KeyClientServer":           KeyClientServer,
		"KeyClientProfile":          KeyClientProfile,
		"KeyClientNodeID":           KeyClientNodeID,
		"KeyClientPKICA":            KeyClientPKICA,
		"KeyClientPKICert":          KeyClientPKICert,
		"KeyClientPKIKey":           KeyClientPKIKey,
		"KeyClientReconnectInitial": KeyClientReconnectInitial,
		"KeyClientReconnectJitter":  KeyClientReconnectJitter,
		"KeyClientReconnectMax":     KeyClientReconnectMax,
		"KeyClientLogLevel":         KeyClientLogLevel,
		"KeyClientLogFormat":        KeyClientLogFormat,
		"FlagConfig":     FlagConfig,
		"FlagAlsoClient": FlagAlsoClient,
		"FlagLogLevel":   FlagLogLevel,
		"FlagLogFormat":  FlagLogFormat,
		"FlagServer":     FlagServer,
		"FlagProfile":    FlagProfile,
		"FlagNodeID":     FlagNodeID,
		"FlagCAPath":     FlagCAPath,
		"FlagOutDir":     FlagOutDir,
		"FlagSAN":        FlagSAN,
		"FlagFailFast":   FlagFailFast,
		"FlagCommonName": FlagCommonName,
	}
}

func TestWrap_IsMatchesSentinelAndInner(t *testing.T) {
	inner := errors.New("boom")
	err := Wrap(inner, ErrApplyFailed, "while applying %s", "foo")
	if !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("errors.Is(ErrApplyFailed) should be true")
	}
	if !errors.Is(err, inner) {
		t.Fatalf("errors.Is(inner) should be true")
	}
	if !strings.Contains(err.Error(), "apply failed") {
		t.Fatalf("want sentinel text in message, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "while applying foo") {
		t.Fatalf("want formatted message, got %q", err.Error())
	}
}

func TestWrap_NilInner(t *testing.T) {
	err := Wrap(nil, ErrConfigInvalid, "bad %s", "yaml")
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("errors.Is should still match sentinel with nil inner")
	}
}
