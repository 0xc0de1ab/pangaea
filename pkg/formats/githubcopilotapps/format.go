// Package githubcopilotapps implements GitHub Copilot credential formats.
//
// Older Copilot SDK builds stored OAuth material in
// ~/.config/github-copilot/apps.json. Newer Copilot CLI builds keep their
// active session in ~/.copilot/config.json, which is implemented by
// ConfigFormat in this package.
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

const (
	FormatName     = "github-copilot-apps-json-format"
	strategyOpaque = "opaque_latest"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

var (
	_ formats.AccountAware        = Format{}
	_ formats.AccountDisplayAware = Format{}
	_ formats.DirResolver         = Format{}
)

type Format struct{}

type appShape struct {
	User        string `json:"user"`
	Login       string `json:"login"`
	Username    string `json:"username"`
	OAuthToken  string `json:"oauth_token"`
	AccessToken string `json:"access_token"`
	Token       string `json:"token"`
	GitHubAppID string `json:"githubAppId"`
}

type appEntry struct {
	Key         string
	Host        string
	GitHubAppID string
	User        string
	Token       string
	TokenTail4  string
}

type snapshot struct {
	raw         []byte
	fingerprint string
	identity    string
	primary     appEntry
	apps        []appEntry
}

func init() {
	formats.Register(Format{})
}

func (Format) Name() string { return FormatName }

func (Format) Strategies() []string { return []string{strategyOpaque} }

func (Format) CredentialPath(dir string) string {
	return filepath.Join(dir, "apps.json")
}

func (Format) Parse(raw []byte) (formats.Snapshot, error) {
	if len(raw) == 0 {
		return nil, common.Wrap(nil, common.ErrParseFailed, "empty github copilot apps.json")
	}
	body := bytes.TrimPrefix(raw, utf8BOM)
	var file map[string]appShape
	if err := json.Unmarshal(body, &file); err != nil {
		return nil, common.Wrap(err, common.ErrParseFailed, "decode github-copilot-apps-json-format")
	}
	keys := make([]string, 0, len(file))
	for key := range file {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	apps := make([]appEntry, 0, len(keys))
	for _, key := range keys {
		shape := file[key]
		token := firstNonBlank(shape.OAuthToken, shape.AccessToken, shape.Token)
		if token == "" {
			continue
		}
		host, appID := splitCopilotAppKey(key)
		if shape.GitHubAppID != "" {
			appID = strings.TrimSpace(shape.GitHubAppID)
		}
		apps = append(apps, appEntry{
			Key:         key,
			Host:        host,
			GitHubAppID: appID,
			User:        firstNonBlank(shape.User, shape.Login, shape.Username),
			Token:       token,
			TokenTail4:  tokenTail(token),
		})
	}
	if len(apps) == 0 {
		return nil, common.Wrap(nil, common.ErrParseFailed, "missing github copilot oauth token")
	}
	rawCopy := append([]byte(nil), raw...)
	fpSum := sha256.Sum256(rawCopy)
	fp := hex.EncodeToString(fpSum[:])
	primary := apps[0]
	idSource := strings.Join([]string{primary.Host, primary.GitHubAppID, primary.User}, "\x00")
	if strings.Trim(idSource, "\x00") == "" {
		idSource = fp
	}
	idSum := sha256.Sum256([]byte(idSource))
	return &snapshot{
		raw:         rawCopy,
		fingerprint: fp,
		identity:    "github-copilot:" + hex.EncodeToString(idSum[:])[:16],
		primary:     primary,
		apps:        apps,
	}, nil
}

func (Format) Validate(_ context.Context, snap formats.Snapshot, opts formats.ValidateOpts) (formats.ValidationResult, error) {
	checkedAt := time.Now().UTC()
	if opts.Clock != nil {
		checkedAt = opts.Clock()
	}
	cs, ok := snap.(*snapshot)
	if !ok || cs == nil || len(cs.apps) == 0 {
		return formats.ValidationResult{Status: formats.StatusParseError, Detail: "missing github copilot app token", CheckedAt: checkedAt}, nil
	}
	return formats.ValidationResult{Status: formats.StatusOK, CheckedAt: checkedAt}, nil
}

func (Format) Compare(strategy string, _, _ formats.Snapshot) int {
	if strategy != strategyOpaque {
		panic(fmt.Sprintf("githubcopilotapps: unknown strategy %q", strategy))
	}
	return 0
}

func (Format) Redact(snap formats.Snapshot) formats.Summary {
	cs, ok := snap.(*snapshot)
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
	if cs.primary.GitHubAppID != "" {
		extra["github_app_id"] = cs.primary.GitHubAppID
	}
	if len(cs.apps) > 1 {
		extra["apps"] = fmt.Sprintf("%d", len(cs.apps))
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

func (Format) Account(ctx context.Context, snap formats.Snapshot, _ string) (string, error) {
	cs, ok := snap.(*snapshot)
	if !ok || cs == nil {
		return "", nil
	}
	user := strings.TrimSpace(cs.primary.User)
	if user != "" {
		return user, nil
	}
	login, err := lookupGitHubLogin(ctx, cs.primary.Host, cs.primary.Token)
	if err != nil || login == "" {
		return "", nil
	}
	return login, nil
}

func (Format) AccountDisplay(ctx context.Context, snap formats.Snapshot, path string) (string, error) {
	return Format{}.Account(ctx, snap, path)
}

func (s *snapshot) Identity() string     { return s.identity }
func (s *snapshot) ExpiresAt() time.Time { return time.Time{} }
func (s *snapshot) Fingerprint() string  { return s.fingerprint }

func (s *snapshot) Raw() []byte {
	return append([]byte(nil), s.raw...)
}

func splitCopilotAppKey(key string) (string, string) {
	key = strings.TrimSpace(key)
	host, appID, ok := strings.Cut(key, ":")
	if !ok {
		return key, ""
	}
	return strings.TrimSpace(host), strings.TrimSpace(appID)
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func tokenTail(token string) string {
	if len(token) <= 4 {
		return token
	}
	return token[len(token)-4:]
}

func shortFingerprint(fp string) string {
	if len(fp) > 12 {
		return fp[:12]
	}
	return fp
}
