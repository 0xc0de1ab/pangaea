package antigravitystate

import (
	"context"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

func (Format) Account(_ context.Context, snap formats.Snapshot, _ string) (string, error) {
	if s, ok := snap.(*snapshot); ok {
		return s.account, nil
	}
	return "", nil
}

func (Format) AccountDisplay(ctx context.Context, snap formats.Snapshot, path string) (string, error) {
	return (Format{}).Account(ctx, snap, path)
}
