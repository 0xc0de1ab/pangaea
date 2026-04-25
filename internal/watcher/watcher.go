package watcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/fsnotify/fsnotify"
)

// Watcher is the minimal surface the agent layer consumes. Implementations
// emit one initial Event per configured path on Start (Exists=true|false)
// and then debounced events for every observed transition.
type Watcher interface {
	Start(ctx context.Context) error
	Events() <-chan Event
	Close() error
}

// New constructs a Watcher over the given candidate paths. Each path's
// parent directory is registered with fsnotify (so temp+rename patterns are
// captured even though they replace the inode).
//
// MVP simplification per tasks §E.7: if a candidate path's parent directory
// does not exist at construction time, New returns an error. Walking up to
// the closest existing ancestor is a v0.2 follow-up.
func New(paths []string, opts Options) (Watcher, error) {
	if len(paths) == 0 {
		return nil, common.Wrap(nil, common.ErrConfigInvalid, "watcher: paths must not be empty")
	}
	opts = opts.withDefaults()

	// Deduplicate and resolve absolute paths so the dir-vs-file filter is reliable.
	abs := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		ap, err := filepath.Abs(p)
		if err != nil {
			return nil, common.Wrap(err, common.ErrConfigInvalid, "watcher: abs path %q", p)
		}
		if _, ok := seen[ap]; ok {
			continue
		}
		seen[ap] = struct{}{}
		abs = append(abs, ap)
	}

	// Validate parent dirs upfront.
	dirs := make(map[string]struct{}, len(abs))
	for _, p := range abs {
		dir := filepath.Dir(p)
		if _, err := os.Stat(dir); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, common.Wrap(err, common.ErrConfigInvalid,
					"watcher: parent directory missing for %q", p)
			}
			return nil, common.Wrap(err, common.ErrConfigInvalid,
				"watcher: stat parent %q", dir)
		}
		dirs[dir] = struct{}{}
	}

	w := &fsWatcher{
		paths:  abs,
		dirs:   dirs,
		opts:   opts,
		events: make(chan Event, opts.MaxQueue),
		done:   make(chan struct{}),
		// pending tracks the time of the latest raw fsnotify event for each path.
		pending: make(map[string]time.Time),
		// queued maps path -> index of the latest queued Event in lastQueued
		// for coalescing on backpressure.
		lastQueued: make(map[string]Event),
	}
	return w, nil
}

// fsWatcher is the concrete implementation. It owns one fsnotify watcher,
// one event-loop goroutine, and a single timer that drives debounce + stable
// window evaluation for all tracked paths.
type fsWatcher struct {
	paths []string
	dirs  map[string]struct{}
	opts  Options

	events chan Event
	done   chan struct{}

	startOnce sync.Once
	closeOnce sync.Once

	fs *fsnotify.Watcher

	mu         sync.Mutex
	pending    map[string]time.Time // path -> last raw event time
	lastQueued map[string]Event     // path -> last queued Event (for coalescing)
}

func (w *fsWatcher) Events() <-chan Event { return w.events }

// Start registers parent directories with fsnotify, emits initial state for
// each candidate path, and begins the event loop. It is non-blocking; the
// loop terminates when ctx is cancelled or Close is called.
func (w *fsWatcher) Start(ctx context.Context) error {
	var startErr error
	w.startOnce.Do(func() {
		fs, err := fsnotify.NewWatcher()
		if err != nil {
			startErr = common.Wrap(err, common.ErrConfigInvalid, "watcher: fsnotify init")
			return
		}
		w.fs = fs
		for d := range w.dirs {
			if err := fs.Add(d); err != nil {
				_ = fs.Close()
				startErr = common.Wrap(err, common.ErrConfigInvalid, "watcher: fsnotify add %q", d)
				return
			}
		}
		// Initial state for each candidate.
		for _, p := range w.paths {
			ev := w.buildEvent(p)
			w.publish(ev)
		}
		go w.loop(ctx)
	})
	return startErr
}

func (w *fsWatcher) Close() error {
	var err error
	w.closeOnce.Do(func() {
		close(w.done)
		if w.fs != nil {
			err = w.fs.Close()
		}
	})
	return err
}

// buildEvent stats path and produces an Event reflecting the current state.
func (w *fsWatcher) buildEvent(p string) Event {
	st, err := os.Stat(p)
	if err != nil {
		return Event{Path: p, Exists: false}
	}
	return Event{
		Path:       p,
		Exists:     true,
		ModifiedAt: st.ModTime(),
		Size:       st.Size(),
	}
}

// publish enqueues an Event on the events channel. If the channel is full
// it coalesces by replacing the most recent queued event for the same path.
//
// Implementation note: Go channels do not allow random-access replacement;
// we approximate coalescing by draining one element and pushing the new one.
// In practice the typical consumer is well above producer cadence so the
// channel rarely saturates. The lastQueued map is informational for tests.
func (w *fsWatcher) publish(ev Event) {
	w.mu.Lock()
	w.lastQueued[ev.Path] = ev
	w.mu.Unlock()
	select {
	case w.events <- ev:
		return
	default:
	}
	// Backpressure: drop oldest, then push the latest.
	for {
		select {
		case <-w.events:
		default:
		}
		select {
		case w.events <- ev:
			return
		default:
			// Channel still full; loop drains again. This is bounded because
			// we are the sole producer.
		}
	}
}

// loop multiplexes fsnotify events with a debounce timer. When a raw event
// arrives for a tracked path, we mark it pending and wake the timer; the
// timer fires every DebounceCore and only emits paths whose stat() result
// has been stable for >= StableWindow.
func (w *fsWatcher) loop(ctx context.Context) {
	tick := time.NewTicker(w.opts.DebounceCore)
	defer tick.Stop()

	// stable tracks the previous (size,mtime) sample per path while we
	// wait for the stable window to expire.
	type sample struct {
		t       time.Time
		size    int64
		modTime time.Time
		exists  bool
	}
	stable := map[string]sample{}

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case ev, ok := <-w.fs.Events:
			if !ok {
				return
			}
			// Match raw event to one of our tracked paths.
			for _, p := range w.paths {
				if ev.Name == p {
					w.mu.Lock()
					w.pending[p] = w.opts.Clock()
					w.mu.Unlock()
					// Reset stable sample so we re-measure.
					delete(stable, p)
				}
			}
		case _, ok := <-w.fs.Errors:
			if !ok {
				return
			}
			// Errors are non-fatal at this layer; the next tick re-evaluates.
		case <-tick.C:
			now := w.opts.Clock()
			w.mu.Lock()
			pending := make([]string, 0, len(w.pending))
			for p, t := range w.pending {
				if now.Sub(t) >= w.opts.DebounceCore {
					pending = append(pending, p)
				}
			}
			w.mu.Unlock()
			for _, p := range pending {
				st, err := os.Stat(p)
				exists := err == nil
				var size int64
				var mt time.Time
				if exists {
					size = st.Size()
					mt = st.ModTime()
				}
				prev, had := stable[p]
				sameSample := had && prev.exists == exists && prev.size == size && prev.modTime.Equal(mt)
				if sameSample && now.Sub(prev.t) >= w.opts.StableWindow {
					// Stable: emit and drop pending.
					ev := Event{Path: p, Exists: exists, Size: size, ModifiedAt: mt}
					w.publish(ev)
					delete(stable, p)
					w.mu.Lock()
					delete(w.pending, p)
					w.mu.Unlock()
					continue
				}
				if sameSample {
					// Sample unchanged but stable window not yet elapsed; keep prev.t.
					continue
				}
				// First sample, or sample changed → reset the stability clock.
				stable[p] = sample{t: now, size: size, modTime: mt, exists: exists}
			}
		}
	}
}

// String is a small helper for diagnostics; included here to keep test files
// from importing fmt for the same.
func (w *fsWatcher) String() string {
	return fmt.Sprintf("fsWatcher(paths=%d dirs=%d)", len(w.paths), len(w.dirs))
}
