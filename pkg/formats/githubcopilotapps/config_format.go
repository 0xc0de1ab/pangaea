package githubcopilotapps

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

const ConfigFormatName = "github-copilot-config-json-format"

var (
	_ formats.AccountAware        = ConfigFormat{}
	_ formats.AccountDisplayAware = ConfigFormat{}
	_ formats.DirResolver         = ConfigFormat{}
)

type ConfigFormat struct{}

type configUser struct {
	Host  string `json:"host"`
	Login string `json:"login"`
}

type configShape struct {
	LastLoggedInUser configUser        `json:"lastLoggedInUser"`
	LoggedInUsers    []configUser      `json:"loggedInUsers"`
	CopilotTokens    map[string]string `json:"copilotTokens"`
	CopilotToken     map[string]string `json:"copilotToken"`
}

type configSnapshot struct {
	raw         []byte
	fingerprint string
	identity    string
	primary     appEntry
	users       []configUser
}

func init() {
	formats.Register(ConfigFormat{})
}

func (ConfigFormat) Name() string { return ConfigFormatName }

func (ConfigFormat) Strategies() []string { return []string{strategyOpaque} }

func (ConfigFormat) CredentialPath(dir string) string {
	return filepath.Join(dir, ".copilot", "config.json")
}

func (ConfigFormat) Parse(raw []byte) (formats.Snapshot, error) {
	if len(raw) == 0 {
		return nil, common.Wrap(nil, common.ErrParseFailed, "empty github copilot config.json")
	}
	body := bytes.TrimPrefix(stripWholeLineJSONComments(raw), utf8BOM)
	var file configShape
	if err := json.Unmarshal(body, &file); err != nil {
		return nil, common.Wrap(err, common.ErrParseFailed, "decode github-copilot-config-json-format")
	}
	primaryUser := firstConfigUser(file.LastLoggedInUser, file.LoggedInUsers)
	token, tokenKey := selectConfigToken(configTokens(file), primaryUser)
	if strings.TrimSpace(token) == "" {
		return nil, common.Wrap(nil, common.ErrParseFailed, "missing github copilot token")
	}
	if primaryUser.Host == "" || primaryUser.Login == "" {
		primaryUser = userFromConfigTokenKey(tokenKey)
	}
	host := strings.TrimSpace(primaryUser.Host)
	if host == "" {
		host = "https://github.com"
	}
	user := strings.TrimSpace(primaryUser.Login)
	rawCopy := append([]byte(nil), raw...)
	fpSum := sha256.Sum256(rawCopy)
	fp := hex.EncodeToString(fpSum[:])
	idSource := strings.Join([]string{host, user}, "\x00")
	if strings.Trim(idSource, "\x00") == "" {
		idSource = fp
	}
	idSum := sha256.Sum256([]byte(idSource))
	return &configSnapshot{
		raw:         rawCopy,
		fingerprint: fp,
		identity:    "github-copilot:" + hex.EncodeToString(idSum[:])[:16],
		primary: appEntry{
			Key:        tokenKey,
			Host:       host,
			User:       user,
			Token:      token,
			TokenTail4: tokenTail(token),
		},
		users: normalizedConfigUsers(file.LastLoggedInUser, file.LoggedInUsers),
	}, nil
}

func (ConfigFormat) Validate(_ context.Context, snap formats.Snapshot, opts formats.ValidateOpts) (formats.ValidationResult, error) {
	checkedAt := time.Now().UTC()
	if opts.Clock != nil {
		checkedAt = opts.Clock()
	}
	cs, ok := snap.(*configSnapshot)
	if !ok || cs == nil || strings.TrimSpace(cs.primary.Token) == "" {
		return formats.ValidationResult{Status: formats.StatusParseError, Detail: "missing github copilot token", CheckedAt: checkedAt}, nil
	}
	return formats.ValidationResult{Status: formats.StatusOK, CheckedAt: checkedAt}, nil
}

func (ConfigFormat) Compare(strategy string, _, _ formats.Snapshot) int {
	if strategy != strategyOpaque {
		panic(fmt.Sprintf("githubcopilotapps: unknown strategy %q", strategy))
	}
	return 0
}

func (ConfigFormat) Redact(snap formats.Snapshot) formats.Summary {
	cs, ok := snap.(*configSnapshot)
	if !ok || cs == nil {
		fp := snap.Fingerprint()
		return formats.Summary{Identity: snap.Identity(), FingerprintShort: shortFingerprint(fp), ExpiresAt: snap.ExpiresAt()}
	}
	extra := map[string]string{}
	if cs.primary.User != "" {
		extra["user"] = cs.primary.User
	}
	if cs.primary.Host != "" {
		extra["host"] = cs.primary.Host
	}
	if len(cs.users) > 1 {
		extra["users"] = fmt.Sprintf("%d", len(cs.users))
	}
	if len(extra) == 0 {
		extra = nil
	}
	return formats.Summary{
		Identity:         cs.identity,
		FingerprintShort: shortFingerprint(cs.fingerprint),
		TokenTail4:       cs.primary.TokenTail4,
		Extra:            extra,
	}
}

func (ConfigFormat) Account(_ context.Context, snap formats.Snapshot, _ string) (string, error) {
	cs, ok := snap.(*configSnapshot)
	if !ok || cs == nil {
		return "", nil
	}
	return strings.TrimSpace(cs.primary.User), nil
}

func (ConfigFormat) AccountDisplay(ctx context.Context, snap formats.Snapshot, path string) (string, error) {
	return ConfigFormat{}.Account(ctx, snap, path)
}

func (s *configSnapshot) Identity() string     { return s.identity }
func (s *configSnapshot) ExpiresAt() time.Time { return time.Time{} }
func (s *configSnapshot) Fingerprint() string  { return s.fingerprint }

func (s *configSnapshot) Raw() []byte {
	return append([]byte(nil), s.raw...)
}

func stripWholeLineJSONComments(raw []byte) []byte {
	lines := bytes.Split(raw, []byte("\n"))
	out := make([][]byte, 0, len(lines))
	for _, line := range lines {
		if bytes.HasPrefix(bytes.TrimSpace(line), []byte("//")) {
			continue
		}
		out = append(out, line)
	}
	return bytes.Join(out, []byte("\n"))
}

func firstConfigUser(last configUser, users []configUser) configUser {
	if strings.TrimSpace(last.Login) != "" || strings.TrimSpace(last.Host) != "" {
		return configUser{Host: strings.TrimSpace(last.Host), Login: strings.TrimSpace(last.Login)}
	}
	for _, user := range users {
		if strings.TrimSpace(user.Login) != "" || strings.TrimSpace(user.Host) != "" {
			return configUser{Host: strings.TrimSpace(user.Host), Login: strings.TrimSpace(user.Login)}
		}
	}
	return configUser{}
}

func configTokens(file configShape) map[string]string {
	if len(file.CopilotTokens) == 0 {
		return file.CopilotToken
	}
	if len(file.CopilotToken) == 0 {
		return file.CopilotTokens
	}
	merged := make(map[string]string, len(file.CopilotTokens)+len(file.CopilotToken))
	for key, token := range file.CopilotTokens {
		merged[key] = token
	}
	for key, token := range file.CopilotToken {
		if strings.TrimSpace(merged[key]) == "" {
			merged[key] = token
		}
	}
	return merged
}

func selectConfigToken(tokens map[string]string, user configUser) (string, string) {
	if len(tokens) == 0 {
		return "", ""
	}
	host := strings.TrimSpace(user.Host)
	login := strings.TrimSpace(user.Login)
	if host != "" && login != "" {
		key := host + ":" + login
		if token := strings.TrimSpace(tokens[key]); token != "" {
			return token, key
		}
	}
	keys := make([]string, 0, len(tokens))
	for key := range tokens {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if login != "" {
		suffix := ":" + login
		for _, key := range keys {
			if strings.HasSuffix(key, suffix) {
				if token := strings.TrimSpace(tokens[key]); token != "" {
					return token, key
				}
			}
		}
	}
	for _, key := range keys {
		if token := strings.TrimSpace(tokens[key]); token != "" {
			return token, key
		}
	}
	return "", ""
}

func userFromConfigTokenKey(key string) configUser {
	key = strings.TrimSpace(key)
	if key == "" {
		return configUser{}
	}
	idx := strings.LastIndex(key, ":")
	if idx < 0 {
		return configUser{Host: key}
	}
	return configUser{
		Host:  strings.TrimSpace(key[:idx]),
		Login: strings.TrimSpace(key[idx+1:]),
	}
}

func normalizedConfigUsers(last configUser, users []configUser) []configUser {
	seen := map[string]struct{}{}
	out := []configUser{}
	add := func(user configUser) {
		user.Host = strings.TrimSpace(user.Host)
		user.Login = strings.TrimSpace(user.Login)
		if user.Host == "" && user.Login == "" {
			return
		}
		key := user.Host + "\x00" + user.Login
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, user)
	}
	add(last)
	for _, user := range users {
		add(user)
	}
	return out
}
