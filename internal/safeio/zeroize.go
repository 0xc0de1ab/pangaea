package safeio

// Zeroize overwrites the byte slice in place with zeros. A nil slice is a
// no-op. Callers must ensure no live aliases (sub-slices, string conversions
// captured elsewhere) retain copies — Go strings are immutable and any
// prior string(b) conversion made its own heap copy that this function
// cannot reach.
func Zeroize(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
