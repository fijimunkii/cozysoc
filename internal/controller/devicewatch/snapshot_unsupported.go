//go:build !darwin

package devicewatch

import "context"

type unsupportedSnapshotter struct{}

func NewSystemSnapshotter() Snapshotter {
	return unsupportedSnapshotter{}
}

func (unsupportedSnapshotter) Snapshot(context.Context, string) (Snapshot, error) {
	return Snapshot{}, ErrSnapshotUnavailable
}
