// Package watcher wraps fsnotify with a small debounce + stable-window state
// machine tailored to credential-file editors that use the temp+rename
// pattern. Watch is performed at the parent-directory level so inode
// replacement does not break the registration; events are filtered down to
// the configured candidate paths before being emitted.
package watcher

import "time"

// Event reports a transition observed for one of the candidate paths.
//
// Exists=true with non-zero ModifiedAt and Size means the file was visible
// at that point. Exists=false means the file was missing at that point —
// callers usually translate this to a snapshot.absent report.
type Event struct {
	Path       string
	Exists     bool
	ModifiedAt time.Time
	Size       int64
}
