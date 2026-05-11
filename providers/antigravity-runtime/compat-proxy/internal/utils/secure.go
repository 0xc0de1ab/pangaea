package utils

import "runtime"

// SecureBytes is a wrapper for a byte slice that attempts to wipe the memory
// when it's no longer needed.
type SecureBytes struct {
	Data []byte
}

// NewSecureBytes creates a new SecureBytes wrapper around a copy of the input.
func NewSecureBytes(data []byte) *SecureBytes {
	b := make([]byte, len(data))
	copy(b, data)
	return &SecureBytes{Data: b}
}

// Wipe zeroes out the underlying byte slice.
func (s *SecureBytes) Wipe() {
	if s.Data == nil {
		return
	}
	for i := range s.Data {
		s.Data[i] = 0
	}
}

// Clear is a helper that can be used with defer.
func (s *SecureBytes) Clear() {
	s.Wipe()
}

// WipeBytes is a utility function to zero out a byte slice.
func WipeBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// RunFinalizerWipe sets a runtime finalizer to wipe the bytes when the object is GC'd.
// NOTE: Finalizers are not guaranteed to run immediately or at all in some cases.
func (s *SecureBytes) RunFinalizerWipe() {
	runtime.SetFinalizer(s, func(sb *SecureBytes) {
		sb.Wipe()
	})
}
