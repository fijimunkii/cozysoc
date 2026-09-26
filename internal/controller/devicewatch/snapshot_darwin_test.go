//go:build darwin

package devicewatch

import (
	"context"
	"errors"
	"testing"
	"time"
)

type permissionRunner struct {
	arpErr error
	ndpErr error
}

func (r permissionRunner) Run(_ context.Context, path string, _ ...string) ([]byte, error) {
	if path == darwinARPPath {
		return nil, r.arpErr
	}
	return nil, r.ndpErr
}

func TestDarwinSnapshotterDistinguishesConfirmedPermissionFailure(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	partial := &darwinSnapshotter{runner: permissionRunner{arpErr: ErrSnapshotPermission}, now: func() time.Time { return now }}
	snapshot, err := partial.Snapshot(context.Background(), "en0")
	if err != nil || len(snapshot.Sources) != 2 || !snapshot.Sources[0].PermissionRequired || snapshot.Sources[0].Available || !snapshot.Sources[1].Available {
		t.Fatalf("partial permission snapshot = %+v, err=%v", snapshot, err)
	}
	both := &darwinSnapshotter{runner: permissionRunner{arpErr: ErrSnapshotPermission, ndpErr: ErrSnapshotPermission}, now: func() time.Time { return now }}
	if _, err := both.Snapshot(context.Background(), "en0"); !errors.Is(err, ErrSnapshotPermission) {
		t.Fatalf("both denied = %v", err)
	}
	unknown := &darwinSnapshotter{runner: permissionRunner{arpErr: ErrSnapshotPermission, ndpErr: ErrSnapshotUnavailable}, now: func() time.Time { return now }}
	_, err = unknown.Snapshot(context.Background(), "en0")
	var failure *snapshotFailure
	if !errors.Is(err, ErrSnapshotUnavailable) || !errors.As(err, &failure) || !failure.Sources[0].PermissionRequired || failure.Sources[1].PermissionRequired {
		t.Fatalf("mixed failure lost per-source state: %v", err)
	}
}
