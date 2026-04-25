package safeio

import (
	"os"
	"path/filepath"

	"github.com/0xc0de1ab/pangaea/internal/common"
)

// Rollback restores dst from a backup path by rename. The backup file is
// consumed (moved) on success. Parent dir is fsynced best-effort.
func Rollback(backupPath, dst string) error {
	if _, err := os.Stat(backupPath); err != nil {
		return common.Wrap(err, common.ErrApplyFailed, "backup missing %s", backupPath)
	}
	if err := os.Rename(backupPath, dst); err != nil {
		return common.Wrap(err, common.ErrApplyFailed, "rename %s -> %s", backupPath, dst)
	}
	if f, err := os.Open(filepath.Dir(dst)); err == nil {
		_ = f.Sync()
		_ = f.Close()
	}
	return nil
}
