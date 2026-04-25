package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/internal/transport"
	"github.com/stretchr/testify/require"
)

// sampleCreds returns a well-formed credentials blob with the given expiry.
func sampleCreds(exp time.Time, tail string) []byte {
	body := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":      "sk-ant-oat01-" + strings.Repeat("X", 40) + tail,
			"refreshToken":     "sk-ant-ort01-" + strings.Repeat("Y", 40),
			"expiresAt":        exp.UnixMilli(),
			"scopes":           []string{"user:inference"},
			"subscriptionType": "max",
		},
	}
	b, _ := json.Marshal(body)
	return b
}

// TestApplyTruth_HappyPath writes a new credentials file to a candidate path.
func TestApplyTruth_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".credentials.json")
	data := sampleCreds(time.Now().Add(time.Hour), "NEW")
	push := transport.TruthPush{
		Profile:     "p1",
		Format:      "claude-credentials-json-format",
		Fingerprint: hashHex(data),
		RawB64:      base64.StdEncoding.EncodeToString(data),
		TargetPath:  path,
	}
	ack := applyTruth(context.Background(), logging.New(logging.Options{Level: "error"}), push, path)
	require.True(t, ack.OK, "ack reason: %s", ack.Reason)
	require.Equal(t, push.Fingerprint, ack.Fingerprint)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, data, got)
	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode()&0o777, "file must be 0600")
}

// TestApplyTruth_AlreadyMatches is a no-op when the file already holds the
// desired bytes.
func TestApplyTruth_AlreadyMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".credentials.json")
	data := sampleCreds(time.Now().Add(time.Hour), "SAME")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	mtime0, err := os.Stat(path)
	require.NoError(t, err)
	origMtime := mtime0.ModTime()

	// Sleep briefly so a re-write would produce a later mtime.
	time.Sleep(50 * time.Millisecond)

	push := transport.TruthPush{
		Profile:     "p1",
		Fingerprint: hashHex(data),
		RawB64:      base64.StdEncoding.EncodeToString(data),
		TargetPath:  path,
	}
	ack := applyTruth(context.Background(), logging.New(logging.Options{Level: "error"}), push, path)
	require.True(t, ack.OK)
	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, origMtime, st.ModTime(), "file should not have been rewritten on no-op")
}

// TestApplyTruth_FallsBackToCandidatePath ignores a foreign target_path and
// still applies to the local credential path so nodes with different local
// layouts converge.
func TestApplyTruth_FallsBackToCandidatePath(t *testing.T) {
	dir := t.TempDir()
	candidate := filepath.Join(dir, ".credentials.json")
	data := sampleCreds(time.Now().Add(time.Hour), "NEW2")
	push := transport.TruthPush{
		Profile:     "p1",
		Fingerprint: hashHex(data),
		RawB64:      base64.StdEncoding.EncodeToString(data),
		TargetPath:  "/not/the/local/path",
	}
	ack := applyTruth(context.Background(), logging.New(logging.Options{Level: "error"}), push, candidate)
	require.True(t, ack.OK)
	got, err := os.ReadFile(candidate)
	require.NoError(t, err)
	require.Equal(t, data, got)
}

// TestApplyTruth_InvalidRawB64 returns ok=false with a descriptive reason.
func TestApplyTruth_InvalidRawB64(t *testing.T) {
	ack := applyTruth(context.Background(), logging.New(logging.Options{Level: "error"}), transport.TruthPush{
		Profile:     "p1",
		Fingerprint: "deadbeef",
		RawB64:      "!!!not-base64!!!",
		TargetPath:  "/tmp/x",
	}, "/tmp/x")
	require.False(t, ack.OK)
	require.NotEmpty(t, ack.Reason)
}

// TestApplyTruth_FingerprintMismatch rolls back when post-write verification
// fails. Simulated by supplying a fingerprint that does not match raw.
func TestApplyTruth_FingerprintMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".credentials.json")
	existing := sampleCreds(time.Now().Add(time.Hour), "OLD")
	require.NoError(t, os.WriteFile(path, existing, 0o600))

	data := sampleCreds(time.Now().Add(2*time.Hour), "NEW")
	push := transport.TruthPush{
		Profile:     "p1",
		Fingerprint: "deadbeef", // wrong on purpose
		RawB64:      base64.StdEncoding.EncodeToString(data),
		TargetPath:  path,
	}
	ack := applyTruth(context.Background(), logging.New(logging.Options{Level: "error"}), push, path)
	require.False(t, ack.OK)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, existing, got, "rollback must restore previous content")
}
