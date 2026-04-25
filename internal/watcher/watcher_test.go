package watcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
)

// fastOptions makes the debounce/stable windows small so tests don't drag.
func fastOptions() Options {
	return Options{
		DebounceCore: 10 * time.Millisecond,
		StableWindow: 30 * time.Millisecond,
		MaxQueue:     16,
	}
}

func startWatcher(t *testing.T, paths []string, opts Options) (Watcher, context.CancelFunc) {
	t.Helper()
	w, err := New(paths, opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := w.Start(ctx); err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = w.Close()
	})
	return w, cancel
}

// awaitEvent reads one Event matching path within timeout. Initial Exists
// reports for OTHER paths are skipped silently.
func awaitEvent(t *testing.T, w Watcher, path string, timeout time.Duration) Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-w.Events():
			if !ok {
				t.Fatalf("events channel closed while waiting for %q", path)
			}
			if ev.Path == path {
				return ev
			}
		case <-deadline:
			t.Fatalf("timeout waiting for event on %q", path)
		}
	}
}

func TestNew_MissingParentDir_ReturnsError(t *testing.T) {
	bogus := filepath.Join(t.TempDir(), "nope-not-real", "f.json")
	_, err := New([]string{bogus}, fastOptions())
	if err == nil {
		t.Fatalf("expected error for missing parent dir")
	}
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("expected ErrConfigInvalid, got %v", err)
	}
}

func TestStart_InitialEvent_FileExists(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "creds.json")
	if err := os.WriteFile(p, []byte(`{"v":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	w, _ := startWatcher(t, []string{p}, fastOptions())

	ev := awaitEvent(t, w, p, time.Second)
	if !ev.Exists {
		t.Fatalf("expected Exists=true, got %+v", ev)
	}
	if ev.Size == 0 {
		t.Fatalf("expected non-zero size, got %+v", ev)
	}
}

func TestStart_InitialEvent_FileMissing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "creds.json")

	w, _ := startWatcher(t, []string{p}, fastOptions())
	ev := awaitEvent(t, w, p, time.Second)
	if ev.Exists {
		t.Fatalf("expected Exists=false, got %+v", ev)
	}

	// Now create the file and expect a single Exists=true event after debounce.
	if err := os.WriteFile(p, []byte(`{"v":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ev2 := awaitEvent(t, w, p, 2*time.Second)
	if !ev2.Exists {
		t.Fatalf("expected Exists=true after create, got %+v", ev2)
	}
}

func TestTempRenamePattern_SingleEvent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "creds.json")
	if err := os.WriteFile(p, []byte(`{"v":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	w, _ := startWatcher(t, []string{p}, fastOptions())
	_ = awaitEvent(t, w, p, time.Second) // consume initial

	tmp := filepath.Join(dir, "creds.json.tmp.abc")
	if err := os.WriteFile(tmp, []byte(`{"v":2,"x":"longer"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, p); err != nil {
		t.Fatal(err)
	}
	ev := awaitEvent(t, w, p, 2*time.Second)
	if !ev.Exists {
		t.Fatalf("expected Exists=true after rename, got %+v", ev)
	}
	// Drain any straggling events for a brief window; we should not see a
	// flurry — at most one extra is acceptable.
	extras := 0
	deadline := time.After(150 * time.Millisecond)
drain:
	for {
		select {
		case e, ok := <-w.Events():
			if !ok {
				break drain
			}
			if e.Path == p {
				extras++
			}
		case <-deadline:
			break drain
		}
	}
	if extras > 1 {
		t.Fatalf("expected at most 1 extra event, got %d", extras)
	}
}

func TestRapidWrites_Coalesce(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "creds.json")
	if err := os.WriteFile(p, []byte(`a`), 0o600); err != nil {
		t.Fatal(err)
	}
	w, _ := startWatcher(t, []string{p}, fastOptions())
	_ = awaitEvent(t, w, p, time.Second) // consume initial

	for i := 0; i < 10; i++ {
		_ = os.WriteFile(p, []byte("data-"+time.Now().Format(time.StampNano)), 0o600)
		time.Sleep(3 * time.Millisecond)
	}

	// Wait for the stable window to elapse, then count events.
	first := awaitEvent(t, w, p, 2*time.Second)
	if !first.Exists {
		t.Fatalf("expected Exists=true after writes")
	}
	extras := 0
	deadline := time.After(250 * time.Millisecond)
drain:
	for {
		select {
		case e, ok := <-w.Events():
			if !ok {
				break drain
			}
			if e.Path == p {
				extras++
			}
		case <-deadline:
			break drain
		}
	}
	// Allow up to 1 extra coalescing wobble across OSes.
	if extras > 1 {
		t.Fatalf("expected coalesced events, got %d extras", extras)
	}
}

func TestDelete_EmitsExistsFalse(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "creds.json")
	if err := os.WriteFile(p, []byte(`x`), 0o600); err != nil {
		t.Fatal(err)
	}
	w, _ := startWatcher(t, []string{p}, fastOptions())
	_ = awaitEvent(t, w, p, time.Second)

	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	ev := awaitEvent(t, w, p, 2*time.Second)
	if ev.Exists {
		t.Fatalf("expected Exists=false after delete, got %+v", ev)
	}
}
