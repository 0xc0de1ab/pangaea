package watcher

import (
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
)

// Options tunes the watcher's timing and queueing behavior. Zero values fall
// back to the defaults baked into internal/common.
type Options struct {
	// DebounceCore: minimum quiet time after the last raw fsnotify event
	// before we begin sampling for stability. Default: common.WatcherDebounceCore.
	DebounceCore time.Duration
	// StableWindow: required duration of stable size between two stat()
	// samples before emitting an Event. Default: common.WatcherStableWindow.
	StableWindow time.Duration
	// PollInterval: stat-based fallback for filesystems that do not reliably
	// emit fsnotify events, such as WSL DrvFs paths under /mnt/c.
	// Default: common.WatcherPollInterval. Set negative to disable.
	PollInterval time.Duration
	// MaxQueue: capacity of the Events() channel. Default: common.WatcherDefaultQueue.
	MaxQueue int
	// Clock: optional injection for tests. Default: time.Now.
	Clock func() time.Time
}

func (o Options) withDefaults() Options {
	if o.DebounceCore <= 0 {
		o.DebounceCore = common.WatcherDebounceCore
	}
	if o.StableWindow <= 0 {
		o.StableWindow = common.WatcherStableWindow
	}
	if o.PollInterval == 0 {
		o.PollInterval = common.WatcherPollInterval
	}
	if o.MaxQueue <= 0 {
		o.MaxQueue = common.WatcherDefaultQueue
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	return o
}
