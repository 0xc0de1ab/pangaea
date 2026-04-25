// Package client implements the node agent: it watches one primary
// credentials file plus optional auxiliary files, reports snapshots to the
// mediating server over an mTLS WebSocket, and applies truth.push messages
// received from the server by atomically writing the delivered bytes to the
// configured target path.
//
// The package is import-safe for the server (server/selfclient.go calls
// client.Run for --also-client); it has no import of internal/server.
package client

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/internal/safeio"
	"github.com/0xc0de1ab/pangaea/internal/transport"
)

// applyTruth is invoked when the session receives a truth.push. It writes the
// delivered bytes atomically to the applicable target path, verifies the
// on-disk fingerprint matches the delivered one, and returns a truth.ack
// payload reflecting the outcome.
//
// The server's target_path is advisory. If it does not match the local
// configured credential path, we still apply to the local path so nodes with
// different directory layouts converge.
func applyTruth(ctx context.Context, log *slog.Logger, push transport.TruthPush, candidatePath string) transport.TruthAck {
	raw, err := base64.StdEncoding.DecodeString(push.RawB64)
	if err != nil {
		return transport.TruthAck{
			Profile:     push.Profile,
			Fingerprint: push.Fingerprint,
			OK:          false,
			Reason:      "invalid raw_b64: " + err.Error(),
		}
	}
	defer safeio.Zeroize(raw)

	target := candidatePath
	if push.TargetPath == candidatePath {
		target = push.TargetPath
	}
	if target == "" {
		return transport.TruthAck{
			Profile:     push.Profile,
			Fingerprint: push.Fingerprint,
			OK:          false,
			Reason:      "candidate path is empty",
		}
	}
	if err := writeOne(ctx, log, push.Fingerprint, raw, target); err != nil {
		return transport.TruthAck{
			Profile:     push.Profile,
			Fingerprint: push.Fingerprint,
			OK:          false,
			Reason:      err.Error(),
		}
	}
	log.Info("truth applied",
		slog.String(logging.FieldEvent, logging.EvtTruthApplied),
		slog.String(logging.FieldFingerprint, push.Fingerprint),
		slog.String(logging.FieldPath, target),
		slog.String(logging.FieldOutcome, logging.OutcomeOK),
	)
	return transport.TruthAck{
		Profile:     push.Profile,
		Fingerprint: push.Fingerprint,
		OK:          true,
	}
}

// writeOne is the per-path apply path: lock → backup → atomic write → verify
// → release. On verify failure the backup is restored.
func writeOne(ctx context.Context, log *slog.Logger, wantFingerprint string, raw []byte, path string) error {
	// Short-circuit: if the file already matches the desired fingerprint we
	// treat it as a no-op. ReadFile may fail for a not-found path; that
	// surfaces as a write-through rather than an early error.
	if cur, err := os.ReadFile(path); err == nil {
		if hashHex(cur) == wantFingerprint {
			return nil
		}
	}

	var unlock func() error
	var lockErr error
	for attempt := 0; attempt < common.LockRetryMax; attempt++ {
		unlock, lockErr = safeio.LockFile(path, common.LockAcquireTimeout)
		if lockErr == nil {
			break
		}
		if !errors.Is(lockErr, common.ErrLockTimeout) {
			return lockErr
		}
		// Small backoff between retries; the Claude CLI typically holds the
		// lock only for milliseconds while writing.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	if lockErr != nil {
		return lockErr
	}
	defer func() { _ = unlock() }()

	// Take a backup of the current file (if it exists). If the file does not
	// exist we skip the backup — there's nothing to restore to.
	var backupPath string
	if _, err := os.Stat(path); err == nil {
		_, bp, err := safeio.ReadWithBackup(path)
		if err != nil {
			return err
		}
		backupPath = bp
	}

	if err := safeio.AtomicWrite(path, raw, common.CredentialsFileMode); err != nil {
		// Attempt rollback from backup on write failure.
		if backupPath != "" {
			_ = restoreBackup(backupPath, path)
		}
		return err
	}

	// Verify fingerprint post-write.
	got, err := os.ReadFile(path)
	if err != nil {
		if backupPath != "" {
			_ = restoreBackup(backupPath, path)
		}
		return common.Wrap(err, common.ErrApplyFailed, "re-read after write")
	}
	if hashHex(got) != wantFingerprint {
		if backupPath != "" {
			_ = restoreBackup(backupPath, path)
		}
		return common.Wrap(nil, common.ErrApplyFailed, common.MsgApplyVerifyFailed)
	}

	// On success, discard the backup.
	if backupPath != "" {
		_ = os.Remove(backupPath)
	}
	_ = ctx
	_ = log
	return nil
}

func restoreBackup(backupPath, dst string) error {
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return err
	}
	if err := safeio.AtomicWrite(dst, data, common.CredentialsFileMode); err != nil {
		return err
	}
	_ = os.Remove(backupPath)
	return nil
}

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
