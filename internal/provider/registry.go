package provider

import (
	"errors"
	"sync"
)

var (
	ErrProviderNotFound  = errors.New("provider not found")
	ErrProviderDuplicate = errors.New("provider duplicate")
)

type Registry struct {
	mu        sync.RWMutex
	providers map[string]Registration
}

func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Registration),
	}
}

func (r *Registry) Upsert(registration Registration) error {
	if err := registration.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[registration.Identity.ProviderInstanceID] = registration
	return nil
}

func (r *Registry) Add(registration Registration) error {
	if err := registration.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	id := registration.Identity.ProviderInstanceID
	if _, ok := r.providers[id]; ok {
		return ErrProviderDuplicate
	}
	r.providers[id] = registration
	return nil
}

func (r *Registry) Get(providerInstanceID string) (Registration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	registration, ok := r.providers[providerInstanceID]
	return registration, ok
}

func (r *Registry) Remove(providerInstanceID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.providers[providerInstanceID]; !ok {
		return false
	}
	delete(r.providers, providerInstanceID)
	return true
}

func (r *Registry) List() []Registration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Registration, 0, len(r.providers))
	for _, registration := range r.providers {
		out = append(out, registration)
	}
	return out
}

func (r *Registry) FindByCapability(capability Capability) []Registration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Registration, 0)
	for _, registration := range r.providers {
		if hasCapability(registration.Capabilities, capability) {
			out = append(out, registration)
		}
	}
	return out
}

func (r *Registry) FindByService(service Service) []Registration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Registration, 0)
	for _, registration := range r.providers {
		if registration.Identity.Service == service {
			out = append(out, registration)
		}
	}
	return out
}

func hasCapability(capabilities []Capability, target Capability) bool {
	for _, capability := range capabilities {
		if capability == target {
			return true
		}
	}
	return false
}
