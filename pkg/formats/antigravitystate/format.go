// Package antigravitystate implements the "antigravity-state-vscdb-format"
// Format for Antigravity's VS Code state database.
//
// Antigravity stores Google OAuth state inside state.vscdb under
// User/globalStorage. This package treats the SQLite database file as the
// credential carrier so Pangaea can sync the exact auth material that the
// Antigravity runtime consumes.
package antigravitystate

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

const FormatName = "antigravity-state-vscdb-format"

const StrategyOAuthExpiryMax = "oauth_expiry_max"

type Format struct{}

func (Format) Name() string { return FormatName }

func (Format) Strategies() []string {
	return []string{StrategyOAuthExpiryMax}
}

func (Format) CredentialPath(dir string) string {
	candidates := candidatePaths(dir)
	for _, path := range candidates {
		if fileExists(path) {
			return path
		}
	}
	for _, path := range candidates {
		if parentExists(path) {
			return path
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return filepath.Join(dir, "state.vscdb")
}

func (Format) WatchPaths(dir string) []string {
	candidates := candidatePaths(dir)
	out := make([]string, 0, len(candidates))
	for _, path := range candidates {
		if parentExists(path) {
			out = append(out, path)
		}
	}
	if len(out) == 0 {
		out = append(out, Format{}.CredentialPath(dir))
	}
	return dedupe(out)
}

func (Format) CredentialPathForEvent(dir string, eventPath string) string {
	eventPath = filepath.Clean(eventPath)
	if eventPath == "." || eventPath == "" {
		return Format{}.CredentialPath(dir)
	}
	for _, path := range candidatePaths(dir) {
		if filepath.Clean(path) == eventPath {
			return eventPath
		}
	}
	return Format{}.CredentialPath(dir)
}

func init() {
	formats.Register(Format{})
}

func strategyMustBeKnown(strategy string) {
	if strategy != StrategyOAuthExpiryMax {
		panic(fmt.Sprintf(
			"antigravitystate: unknown comparison strategy %q (supported: %v); caller must validate against Format.Strategies() before calling Compare",
			strategy, []string{StrategyOAuthExpiryMax},
		))
	}
}

func candidatePaths(dir string) []string {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "." || dir == "" {
		dir = userHomeDir()
	}
	if filepath.Base(dir) == "state.vscdb" {
		return []string{dir}
	}

	candidates := []string{}
	if strings.HasSuffix(filepath.ToSlash(dir), "/User/globalStorage") {
		candidates = append(candidates, filepath.Join(dir, "state.vscdb"))
	} else {
		candidates = append(candidates, linuxStateCandidates(dir)...)
		candidates = append(candidates, wslWindowsStateCandidates()...)
		candidates = append(candidates, filepath.Join(dir, "state.vscdb"))
	}
	return dedupe(candidates)
}

func linuxStateCandidates(home string) []string {
	return []string{
		filepath.Join(home, ".config", "Antigravity", "User", "globalStorage", "state.vscdb"),
		filepath.Join(home, ".antigravity-server", "data", "User", "globalStorage", "state.vscdb"),
	}
}

func wslWindowsStateCandidates() []string {
	if runtime.GOOS != "linux" {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(
		string(os.PathSeparator),
		"mnt",
		"c",
		"Users",
		"*",
		"AppData",
		"Roaming",
		"Antigravity",
		"User",
		"globalStorage",
		"state.vscdb",
	))
	slices.Sort(matches)
	return matches
}

func userHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	return ""
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func parentExists(path string) bool {
	st, err := os.Stat(filepath.Dir(path))
	return err == nil && st.IsDir()
}

func dedupe(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		path = filepath.Clean(path)
		if path == "." || path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}
