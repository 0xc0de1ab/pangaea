package tunnel

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrInvalidPoolConfig = errors.New("tunnel: invalid pool config")
	ErrInvalidAcquire    = errors.New("tunnel: invalid acquire request")
	ErrPoolExhausted     = errors.New("tunnel: stream pool exhausted")
	ErrPoolClosed        = errors.New("tunnel: stream pool closed")
	ErrStreamNotActive   = errors.New("tunnel: stream is not active")
)

type Pool struct {
	mu        sync.Mutex
	maxIdle   int
	maxActive int
	nextID    uint64
	closed    bool
	idle      []StreamDescriptor
	active    map[StreamID]StreamDescriptor
}

type PoolStats struct {
	MaxIdle   int
	MaxActive int
	Idle      int
	Active    int
	Closed    bool
}

func NewPool(maxIdle, maxActive int) (*Pool, error) {
	if maxIdle < 0 {
		return nil, fmt.Errorf("%w: max idle must be non-negative", ErrInvalidPoolConfig)
	}
	if maxActive <= 0 {
		return nil, fmt.Errorf("%w: max active must be positive", ErrInvalidPoolConfig)
	}
	return &Pool{
		maxIdle:   maxIdle,
		maxActive: maxActive,
		active:    make(map[StreamID]StreamDescriptor),
	}, nil
}

func (p *Pool) Acquire(provider ProviderInstanceID, model string) (StreamDescriptor, error) {
	if p == nil {
		return StreamDescriptor{}, ErrPoolClosed
	}
	if blank(string(provider)) {
		return StreamDescriptor{}, fmt.Errorf("%w: provider_instance_id is required", ErrInvalidAcquire)
	}
	if blank(model) {
		return StreamDescriptor{}, fmt.Errorf("%w: model is required", ErrInvalidAcquire)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return StreamDescriptor{}, ErrPoolClosed
	}
	if len(p.active) >= p.maxActive {
		return StreamDescriptor{}, ErrPoolExhausted
	}

	now := time.Now().UTC()
	if idx := p.findIdleLocked(provider, model); idx >= 0 {
		desc := p.idle[idx]
		p.idle = append(p.idle[:idx], p.idle[idx+1:]...)
		desc.State = StateActive
		desc.UpdatedAt = now
		p.active[desc.StreamID] = desc
		return desc, nil
	}

	desc := StreamDescriptor{
		StreamID:           p.newStreamIDLocked(),
		ProviderInstanceID: provider,
		Model:              model,
		State:              StateActive,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	p.active[desc.StreamID] = desc
	return desc, nil
}

func (p *Pool) Release(id StreamID) error {
	if p == nil {
		return ErrPoolClosed
	}
	if blank(string(id)) {
		return fmt.Errorf("%w: stream_id is required", ErrStreamNotActive)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrPoolClosed
	}
	desc, ok := p.active[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrStreamNotActive, id)
	}
	delete(p.active, id)

	now := time.Now().UTC()
	desc.UpdatedAt = now
	if len(p.idle) >= p.maxIdle {
		desc.State = StateClosed
		return nil
	}
	desc.State = StateIdle
	p.idle = append(p.idle, desc)
	return nil
}

func (p *Pool) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closed = true
	p.idle = nil
	p.active = nil
	return nil
}

func (p *Pool) Stats() PoolStats {
	if p == nil {
		return PoolStats{Closed: true}
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	return PoolStats{
		MaxIdle:   p.maxIdle,
		MaxActive: p.maxActive,
		Idle:      len(p.idle),
		Active:    len(p.active),
		Closed:    p.closed,
	}
}

func (p *Pool) findIdleLocked(provider ProviderInstanceID, model string) int {
	for i, desc := range p.idle {
		if desc.Matches(provider, model) {
			return i
		}
	}
	return -1
}

func (p *Pool) newStreamIDLocked() StreamID {
	p.nextID++
	return StreamID(fmt.Sprintf("stream-%d", p.nextID))
}
