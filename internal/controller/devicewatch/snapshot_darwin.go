//go:build darwin

package devicewatch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

const (
	darwinARPPath = "/usr/sbin/arp"
	darwinNDPPath = "/usr/sbin/ndp"
)

type commandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type systemCommandRunner struct{}

type darwinSnapshotter struct {
	runner commandRunner
	now    func() time.Time
}

func NewSystemSnapshotter() Snapshotter {
	return &darwinSnapshotter{runner: systemCommandRunner{}, now: time.Now}
}

func (s *darwinSnapshotter) Snapshot(ctx context.Context, interfaceName string) (Snapshot, error) {
	if err := validateInterfaceName(interfaceName); err != nil {
		return Snapshot{}, err
	}
	capturedAt := s.now().UTC()
	snapshot := Snapshot{
		CapturedAt:    capturedAt,
		InterfaceName: interfaceName,
		Sources: []SourceStatus{
			{Method: MethodARPCache},
			{Method: MethodNDPCache},
		},
	}

	arpOutput, arpErr := s.runner.Run(ctx, darwinARPPath, "-an", "-i", interfaceName)
	if errors.Is(arpErr, ErrSnapshotTooLarge) {
		return Snapshot{}, ErrSnapshotTooLarge
	}
	if arpErr == nil {
		neighbors, err := parseARP(arpOutput, interfaceName)
		if err == nil {
			snapshot.Neighbors = append(snapshot.Neighbors, neighbors...)
			snapshot.Sources[0].Available = true
		}
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}

	ndpOutput, ndpErr := s.runner.Run(ctx, darwinNDPPath, "-an")
	if errors.Is(ndpErr, ErrSnapshotTooLarge) {
		return Snapshot{}, ErrSnapshotTooLarge
	}
	if ndpErr == nil {
		neighbors, err := parseNDP(ndpOutput, interfaceName)
		if err == nil {
			snapshot.Neighbors = append(snapshot.Neighbors, neighbors...)
			snapshot.Sources[1].Available = true
		}
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if !snapshot.Sources[0].Available && !snapshot.Sources[1].Available {
		return Snapshot{}, ErrSnapshotUnavailable
	}
	if len(snapshot.Neighbors) > MaxNeighborEntries {
		return Snapshot{}, ErrSnapshotTooLarge
	}
	snapshot.Neighbors = deduplicateNeighbors(snapshot.Neighbors)
	return snapshot, nil
}

func (systemCommandRunner) Run(ctx context.Context, path string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	stdout := &boundedBuffer{limit: MaxSnapshotBytes}
	stderr := &boundedBuffer{limit: 8 << 10}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if errors.Is(err, ErrSnapshotTooLarge) {
			return nil, ErrSnapshotTooLarge
		}
		return nil, fmt.Errorf("neighbor table utility failed")
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.buffer.Len() {
		return 0, ErrSnapshotTooLarge
	}
	return b.buffer.Write(p)
}

func (b *boundedBuffer) Bytes() []byte {
	return b.buffer.Bytes()
}
