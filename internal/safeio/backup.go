package safeio

import (
	"fmt"
	"os"
	"time"

	"github.com/dh-kam/claude-creds-share/internal/common"
)

// ReadWithBackup reads dst into memory and atomically writes a sibling
// ".bak.<unix-nanos>" copy with 0600, returning the backup path. Caller is
// responsible for eventually unlinking the backup (on successful apply) or
// calling Rollback on failure.
func ReadWithBackup(dst string) ([]byte, string, error) {
	data, err := os.ReadFile(dst)
	if err != nil {
		return nil, "", common.Wrap(err, common.ErrApplyFailed, "read %s", dst)
	}
	backupPath := fmt.Sprintf("%s.bak.%d", dst, time.Now().UnixNano())
	if err := AtomicWrite(backupPath, data, common.CredentialsFileMode); err != nil {
		return nil, "", err
	}
	return data, backupPath, nil
}
