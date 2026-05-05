package nodeagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/safeio"
)

type BootstrapAuthResult struct {
	HostPath       string      `json:"host_path"`
	ContainerPath  string      `json:"container_path"`
	Bytes          int         `json:"bytes"`
	FileMode       os.FileMode `json:"file_mode"`
	BootstrappedAt time.Time   `json:"bootstrapped_at"`
}

func BootstrapAuthCopy(ctx context.Context, auth AuthSpec) (BootstrapAuthResult, error) {
	if err := auth.Validate("bootstrap"); err != nil {
		return BootstrapAuthResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return BootstrapAuthResult{}, err
	}
	data, err := os.ReadFile(auth.HostPath)
	if err != nil {
		return BootstrapAuthResult{}, fmt.Errorf("%w: read host auth: %v", ErrNodeAgentConfig, err)
	}
	if err := ctx.Err(); err != nil {
		return BootstrapAuthResult{}, err
	}
	perm, err := auth.FilePerm()
	if err != nil {
		return BootstrapAuthResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(auth.ContainerPath), 0o700); err != nil {
		return BootstrapAuthResult{}, fmt.Errorf("%w: create auth parent dir: %v", ErrNodeAgentConfig, err)
	}
	if err := safeio.AtomicWrite(auth.ContainerPath, data, perm); err != nil {
		return BootstrapAuthResult{}, err
	}
	if auth.OwnerUID != nil || auth.OwnerGID != nil {
		uid := -1
		gid := -1
		if auth.OwnerUID != nil {
			uid = *auth.OwnerUID
		}
		if auth.OwnerGID != nil {
			gid = *auth.OwnerGID
		}
		if err := os.Chown(auth.ContainerPath, uid, gid); err != nil {
			return BootstrapAuthResult{}, fmt.Errorf("%w: chown auth file: %v", ErrNodeAgentConfig, err)
		}
	}
	return BootstrapAuthResult{
		HostPath:       auth.HostPath,
		ContainerPath:  auth.ContainerPath,
		Bytes:          len(data),
		FileMode:       perm,
		BootstrappedAt: time.Now().UTC(),
	}, nil
}
