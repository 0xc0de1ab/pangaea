package formats

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// fakeFormat is a minimal Format used to exercise the registry. Methods are
// intentionally trivial — registry tests must not depend on Parse/Validate/
// Compare/Redact behaviour.
type fakeFormat struct{ name string }

func (f fakeFormat) Name() string                   { return f.name }
func (f fakeFormat) Strategies() []string           { return nil }
func (f fakeFormat) Parse([]byte) (Snapshot, error) { return nil, nil }
func (f fakeFormat) Validate(context.Context, Snapshot, ValidateOpts) (ValidationResult, error) {
	return ValidationResult{}, nil
}
func (f fakeFormat) Compare(string, Snapshot, Snapshot) int { return 0 }
func (f fakeFormat) Redact(Snapshot) Summary                { return Summary{} }

// withCleanRegistry swaps the package-global registry for an empty one for the
// duration of the test, then restores it. This keeps the production
// registrations from claudecreds (registered via init()) from leaking into the
// table-driven cases.
func withCleanRegistry(t *testing.T) {
	t.Helper()
	registryMu.Lock()
	saved := registry
	registry = map[string]Format{}
	registryMu.Unlock()
	t.Cleanup(func() {
		registryMu.Lock()
		registry = saved
		registryMu.Unlock()
	})
}

func TestRegister_GetRoundTrip(t *testing.T) {
	withCleanRegistry(t)
	f := fakeFormat{name: "fake-format"}
	Register(f)
	got, ok := Get("fake-format")
	if !ok {
		t.Fatalf("Get(%q) ok=false, want true", "fake-format")
	}
	if got.Name() != "fake-format" {
		t.Fatalf("Get(%q).Name()=%q, want %q", "fake-format", got.Name(), "fake-format")
	}
}

func TestGet_MissingReturnsFalse(t *testing.T) {
	withCleanRegistry(t)
	if _, ok := Get("nope"); ok {
		t.Fatalf("Get(%q) ok=true, want false", "nope")
	}
}

func TestRegister_DoublePanics(t *testing.T) {
	withCleanRegistry(t)
	Register(fakeFormat{name: "dup"})
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("Register did not panic on duplicate name")
		}
	}()
	Register(fakeFormat{name: "dup"})
}

func TestRegister_NilPanics(t *testing.T) {
	withCleanRegistry(t)
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("Register(nil) did not panic")
		}
	}()
	Register(nil)
}

func TestRegister_EmptyNamePanics(t *testing.T) {
	withCleanRegistry(t)
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("Register with empty Name() did not panic")
		}
	}()
	Register(fakeFormat{name: ""})
}

func TestList_SortedCopy(t *testing.T) {
	withCleanRegistry(t)
	Register(fakeFormat{name: "b"})
	Register(fakeFormat{name: "a"})
	Register(fakeFormat{name: "c"})

	got := List()
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List()=%v, want %v", got, want)
	}

	// Mutate the returned slice; registry must remain intact.
	got[0] = "ZZZ"
	again := List()
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("List() after caller mutation=%v, want %v (registry leaked its slice)", again, want)
	}
}

// TestList_ConcurrentSafety ensures Register and List can be called from
// different goroutines without triggering -race. It is not a correctness test
// per se; race-free reads + writes are the contract.
func TestList_ConcurrentSafety(t *testing.T) {
	withCleanRegistry(t)
	done := make(chan struct{})
	go func() {
		deadline := time.Now().Add(50 * time.Millisecond)
		i := 0
		for time.Now().Before(deadline) {
			Register(fakeFormat{name: stringName(i)})
			i++
		}
		close(done)
	}()
	for {
		select {
		case <-done:
			return
		default:
			_ = List()
		}
	}
}

// stringName returns a deterministic unique-per-call string without pulling in
// strconv just for a test helper.
func stringName(i int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz"
	if i < len(alphabet) {
		return string(alphabet[i])
	}
	return string(alphabet[i%len(alphabet)]) + stringName(i/len(alphabet)-1)
}
