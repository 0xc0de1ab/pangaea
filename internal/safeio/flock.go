package safeio

import (
	"context"
	"errors"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/gofrs/flock"
)

// LockFile takes an exclusive lock on "<dst>.lock". Returns an unlock function.
// If the timeout elapses without acquisition, common.ErrLockTimeout is returned.
// A zero timeout still makes a single TryLock attempt.
func LockFile(dst string, timeout time.Duration) (func() error, error) {
	lockPath := dst + ".lock"
	lk := flock.New(lockPath)

	if timeout <= 0 {
		ok, err := lk.TryLock()
		if err != nil {
			return nil, common.Wrap(err, common.ErrLockTimeout, "try-lock %s", lockPath)
		}
		if !ok {
			return nil, common.Wrap(nil, common.ErrLockTimeout, common.MsgLockTimeout, time.Duration(0))
		}
		return lk.Unlock, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ok, err := lk.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, common.Wrap(err, common.ErrLockTimeout, common.MsgLockTimeout, timeout)
		}
		return nil, common.Wrap(err, common.ErrLockTimeout, "lock %s", lockPath)
	}
	if !ok {
		return nil, common.Wrap(nil, common.ErrLockTimeout, common.MsgLockTimeout, timeout)
	}
	return lk.Unlock, nil
}
