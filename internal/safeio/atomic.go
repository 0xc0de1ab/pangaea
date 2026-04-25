// Package safeio provides file-safety primitives shared by the server and
// client: atomic writes (temp+fsync+rename), exclusive file locks, a
// best-effort memory zeroizer, and a backup/rollback helper. None of these
// primitives create directories — callers must ensure parents exist.
package safeio

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/dh-kam/claude-creds-share/internal/common"
)

// AtomicWrite writes data to dst atomically: write a sibling temp file with
// the requested mode, fsync, rename into place, then fsync the parent dir.
// Any failure leaves dst unchanged and removes the temp file.
func AtomicWrite(dst string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(dst)

	// Reject writes to a missing parent early so callers get a clear error
	// rather than a confusing "file not found" on the temp path.
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		if err == nil {
			err = errors.New("parent is not a directory")
		}
		return common.Wrap(err, common.ErrApplyFailed, "parent dir inaccessible: %s", dir)
	}

	tmp, err := makeTempPath(dst)
	if err != nil {
		return common.Wrap(err, common.ErrApplyFailed, "temp path alloc")
	}

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return common.Wrap(err, common.ErrApplyFailed, "open temp %s", tmp)
	}
	cleanupTmp := func() { _ = os.Remove(tmp) }

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		cleanupTmp()
		return common.Wrap(err, common.ErrApplyFailed, "write temp")
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		cleanupTmp()
		return common.Wrap(err, common.ErrApplyFailed, "fsync temp")
	}
	if err := f.Close(); err != nil {
		cleanupTmp()
		return common.Wrap(err, common.ErrApplyFailed, "close temp")
	}
	// Some filesystems drop exec bits on create; reassert the intended mode.
	if err := os.Chmod(tmp, perm); err != nil {
		cleanupTmp()
		return common.Wrap(err, common.ErrApplyFailed, "chmod temp")
	}
	if err := os.Rename(tmp, dst); err != nil {
		cleanupTmp()
		return common.Wrap(err, common.ErrApplyFailed, "rename into place")
	}
	// Persist the directory entry. Best-effort: some filesystems return EINVAL
	// on dir Sync, which is acceptable.
	if dirf, derr := os.Open(dir); derr == nil {
		_ = dirf.Sync()
		_ = dirf.Close()
	}
	return nil
}

func makeTempPath(dst string) (string, error) {
	var rnd [4]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", err
	}
	return dst + ".tmp." + hex.EncodeToString(rnd[:]), nil
}

// Ensure fs.FileMode is referenced so `go vet` doesn't flag the import if the
// above signature evolves to drop it.
var _ fs.FileMode
