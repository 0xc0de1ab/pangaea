package formats

import (
	"fmt"
	"sort"
	"sync"
)

// registryMu guards the registry map. The registry itself is intentionally
// unexported so callers cannot mutate it directly; they go through Register/
// Get/List which enforce naming uniqueness and ordering invariants.
var (
	registryMu sync.RWMutex
	registry   = map[string]Format{}
)

// Register adds f under f.Name(). It panics if a Format with the same name is
// already registered — duplicate names are a programmer error (the format
// would otherwise silently shadow another) and surfacing them at init time is
// preferable to debugging a nondeterministic resolution at runtime.
func Register(f Format) {
	if f == nil {
		panic("formats: Register called with nil Format")
	}
	name := f.Name()
	if name == "" {
		panic("formats: Register called with empty Format.Name()")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("formats: Format %q already registered", name))
	}
	registry[name] = f
}

// Get returns the Format registered under name and a boolean indicating
// whether it was found.
func Get(name string) (Format, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	f, ok := registry[name]
	return f, ok
}

// List returns the registered format names in lexicographic order. The
// returned slice is a fresh copy; mutating it has no effect on the registry.
func List() []string {
	registryMu.RLock()
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	registryMu.RUnlock()
	sort.Strings(names)
	return names
}
