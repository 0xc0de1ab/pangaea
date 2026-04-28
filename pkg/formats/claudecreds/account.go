package claudecreds

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// claudeMetaShape mirrors the relevant slice of ~/.claude.json. Claude Code
// writes much more than this; we only care about the OAuth account block.
//
// Source: claude-code's storeAccountInfo writes
//
//	{accountUuid, emailAddress, organizationUuid}
//
// to oauthAccount on login (cli.js, function "OAuthFlow.storeAccountInfo").
// accountUuid is stable across token refreshes — refresh writes only the
// credentials file, never .claude.json. emailAddress is a friendlier
// fallback when accountUuid is somehow absent (e.g. legacy file).
type claudeMetaShape struct {
	OauthAccount struct {
		AccountUUID      string `json:"accountUuid"`
		EmailAddress     string `json:"emailAddress"`
		OrganizationUUID string `json:"organizationUuid"`
	} `json:"oauthAccount"`
}

// Account derives a stable account identifier for claude credentials. The
// credentials file itself carries no account info — we read a peer
// metadata file (Claude Code's ~/.claude.json) to find oauthAccount.
//
// metaPath resolution (first non-empty wins):
//  1. The path argument, if it points at a file we can read as a claude meta
//     blob (operator opted in by setting ProfileBinding.AccountMetaPath).
//  2. The path argument, if it points at a Claude config directory
//     ("~/.claude"), is expanded to the adjacent "~/.claude.json" and the
//     legacy "~/.claude/.config.json".
//  3. The default location: $CLAUDE_CONFIG_DIR/.claude.json if the env var is
//     set, else $HOME/.claude.json.
//  4. Legacy fallback: ~/.claude/.config.json (older Claude Code versions
//     wrote here when CLAUDE_CONFIG_DIR was not set).
//
// A missing file is not an error — the caller (client) treats "" as "no
// account known" and falls back to the shared bucket.
func (Format) Account(_ context.Context, _ formats.Snapshot, path string) (string, error) {
	m, ok, err := readClaudeMeta(path)
	if err != nil || !ok {
		return "", err
	}
	if m.OauthAccount.AccountUUID != "" {
		return m.OauthAccount.AccountUUID, nil
	}
	if m.OauthAccount.EmailAddress != "" {
		return m.OauthAccount.EmailAddress, nil
	}
	return "", nil
}

func (Format) AccountDisplay(_ context.Context, _ formats.Snapshot, path string) (string, error) {
	m, ok, err := readClaudeMeta(path)
	if err != nil || !ok {
		return "", err
	}
	if m.OauthAccount.EmailAddress != "" {
		return m.OauthAccount.EmailAddress, nil
	}
	return "", nil
}

func readClaudeMeta(path string) (claudeMetaShape, bool, error) {
	for _, p := range metaPathCandidates(path) {
		raw, err := os.ReadFile(p)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return claudeMetaShape{}, false, common.Wrap(err, common.ErrParseFailed, "read claude meta %s", p)
		}
		var m claudeMetaShape
		if err := json.Unmarshal(raw, &m); err != nil {
			// Treat parse errors as "no account here, try the next candidate".
			continue
		}
		return m, true, nil
	}
	return claudeMetaShape{}, false, nil
}

// metaPathCandidates returns the ordered list of paths to try when looking
// for the claude meta file. operatorOverride may be empty, an explicit
// AccountMetaPath from client.yaml, a Claude config dir, or a concrete
// credentials path.
func metaPathCandidates(operatorOverride string) []string {
	var out []string
	if operatorOverride != "" {
		if st, err := os.Stat(operatorOverride); err == nil && st.IsDir() {
			out = append(out,
				filepath.Join(filepath.Dir(operatorOverride), ".claude.json"),
				filepath.Join(operatorOverride, ".config.json"),
			)
		} else if filepath.Base(operatorOverride) == ".credentials.json" {
			cfgDir := filepath.Dir(operatorOverride)
			out = append(out,
				filepath.Join(filepath.Dir(cfgDir), ".claude.json"),
				filepath.Join(cfgDir, ".config.json"),
			)
		} else {
			out = append(out, operatorOverride)
		}
	}
	// 2. CLAUDE_CONFIG_DIR/.claude.json
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		out = append(out, filepath.Join(dir, ".claude.json"))
	}
	// 3. $HOME/.claude.json
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out, filepath.Join(home, ".claude.json"))
		// 4. legacy ~/.claude/.config.json
		out = append(out, filepath.Join(home, ".claude", ".config.json"))
	}
	return out
}
