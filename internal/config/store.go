package config

import (
	"sync"
)

// ProfileStore is the runtime accessor for the parsed profiles.yaml. It is
// safe for concurrent use; the server runtime calls Reload from its SIGHUP
// handler while request handlers concurrently call Get/List.
type ProfileStore interface {
	// Get returns the profile with the given name.
	Get(name string) (Profile, bool)
	// List returns a snapshot copy of all profiles, in declaration order.
	List() []Profile
	// Reload re-reads the on-disk file and atomically swaps the in-memory
	// state. On error the previous state is preserved.
	Reload(path string) error
	// Subscribe returns a channel that receives the new profile slice after
	// every successful Reload. The channel is buffered (capacity 1); slow
	// subscribers drop intermediate updates instead of blocking Reload.
	Subscribe() <-chan []Profile
}

type profileStore struct {
	mu          sync.RWMutex
	profiles    []Profile
	subscribers []chan []Profile
}

// NewProfileStore constructs a ProfileStore seeded with the given profiles.
// Pass nil if you want to start empty and Reload later.
func NewProfileStore(initial *ProfilesFile) ProfileStore {
	s := &profileStore{}
	if initial != nil {
		s.profiles = append(s.profiles, initial.Profiles...)
	}
	return s
}

func (s *profileStore) Get(name string) (Profile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.profiles {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

func (s *profileStore) List() []Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Profile, len(s.profiles))
	copy(out, s.profiles)
	return out
}

func (s *profileStore) Reload(path string) error {
	pf, err := LoadProfiles(path)
	if err != nil {
		// Preserve previous state on parse/validation failure (specs §6 panel
		// note Seo: "wrong yaml keeps old config").
		return err
	}
	s.mu.Lock()
	s.profiles = append([]Profile(nil), pf.Profiles...)
	subs := make([]chan []Profile, len(s.subscribers))
	copy(subs, s.subscribers)
	snapshot := append([]Profile(nil), s.profiles...)
	s.mu.Unlock()

	for _, ch := range subs {
		// Non-blocking send so a stalled subscriber never holds up the
		// reload path. Buffer size 1 means a slow consumer always sees the
		// latest snapshot, possibly skipping intermediate ones.
		select {
		case ch <- snapshot:
		default:
			// Drain any prior queued snapshot and queue the latest. This
			// keeps the channel containing the freshest data without
			// blocking.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- snapshot:
			default:
			}
		}
	}
	return nil
}

func (s *profileStore) Subscribe() <-chan []Profile {
	ch := make(chan []Profile, 1)
	s.mu.Lock()
	s.subscribers = append(s.subscribers, ch)
	s.mu.Unlock()
	return ch
}
