package devicewatch

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

// This is a codec feasibility experiment, not the persisted daily workload.
// It retains complete generated records but excludes database indexes, expiry
// bookkeeping, crash recovery, query costs and other controller overhead.
func TestDeviceWatchCompressionFeasibility(t *testing.T) {
	if os.Getenv("COZYSOC_COMPRESSION_EXPERIMENT") == "" {
		t.Skip("set COZYSOC_COMPRESSION_EXPERIMENT=1")
	}
	if os.Getenv("COZYSOC_COMPRESSION_EXPERIMENT") != "1" {
		t.Fatal("COZYSOC_COMPRESSION_EXPERIMENT must be exactly 1")
	}
	for _, level := range []int{gzip.BestSpeed, gzip.DefaultCompression, gzip.BestCompression} {
		t.Run(fmt.Sprintf("level-%d", level), func(t *testing.T) { runCompressionExperiment(t, 1440, level) })
	}
}

func TestDeviceWatchCompressionRoundTrip(t *testing.T) {
	runCompressionExperiment(t, 2, gzip.DefaultCompression)
}

type compressionEvidence struct {
	Observation domain.Observation       `json:"observation"`
	Claims      []domain.IdentityClaim   `json:"claims"`
	Links       []domain.DeviceClaimLink `json:"links"`
}

// Stable 100-device synthetic continuity fixture. It intentionally does not
// implement a replacement for production identity queries or reconciliation.
type compressionIdentityFixture struct {
	current compressionEvidence
	devices map[string]domain.Device
	lastMAC map[string]string
}

func (s *compressionIdentityFixture) EnsureDevice(_ context.Context, d domain.Device) error {
	s.devices[d.ID] = d
	return nil
}
func (s *compressionIdentityFixture) EnsureIdentityClaim(_ context.Context, c domain.IdentityClaim) (string, error) {
	s.current.Claims = append(s.current.Claims, c)
	return c.ID, nil
}
func (s *compressionIdentityFixture) EnsureDeviceClaimLink(_ context.Context, l domain.DeviceClaimLink) error {
	s.current.Links = append(s.current.Links, l)
	for _, c := range s.current.Claims {
		if c.ID == l.ClaimID && c.Kind == domain.ClaimMAC {
			s.lastMAC[c.Value] = l.DeviceID
		}
	}
	return nil
}
func (s *compressionIdentityFixture) FindRecentDevicesByClaim(_ context.Context, _ string, _ domain.ClaimKind, value string, _, _ time.Time) ([]domain.Device, error) {
	if id, ok := s.lastMAC[value]; ok {
		return []domain.Device{s.devices[id]}, nil
	}
	return nil, nil
}

func runCompressionExperiment(t *testing.T, rounds, level int) {
	t.Helper()
	start := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	fixture := &compressionIdentityFixture{devices: map[string]domain.Device{}, lastMAC: map[string]string{}}
	reconciler, err := NewReconciler(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var rawBytes, compressedBytes int64
	var maxRaw, maxCompressed int
	for minute := 0; minute < rounds; minute++ {
		batch := make([]compressionEvidence, 0, 100)
		for i := 0; i < 100; i++ {
			mac, err := net.ParseMAC(fmt.Sprintf("02:00:00:00:00:%02x", i+1))
			if err != nil {
				t.Fatal(err)
			}
			observation, err := buildNeighborObservation("scope.storage-lab", "sensor.storage-lab", start.Add(time.Duration(minute)*time.Minute), Neighbor{Address: netip.MustParseAddr(fmt.Sprintf("192.168.50.%d", i+10)), HardwareAddr: mac, InterfaceName: "fixture0", Method: MethodARPCache})
			if err != nil {
				t.Fatal(err)
			}
			fixture.current = compressionEvidence{Observation: observation}
			result, err := reconciler.ReconcileObservation(context.Background(), observation)
			if err != nil || result.Ambiguous || len(fixture.current.Claims) != 2 || len(fixture.current.Links) != 2 {
				t.Fatal("incomplete compression input", err)
			}
			batch = append(batch, fixture.current)
		}
		raw, err := json.Marshal(batch)
		if err != nil {
			t.Fatal(err)
		}
		var compressed bytes.Buffer
		writer, err := gzip.NewWriterLevel(&compressed, level)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(raw); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		reader, err := gzip.NewReader(bytes.NewReader(compressed.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := io.ReadAll(io.LimitReader(reader, int64(len(raw))+1))
		if err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(raw, decoded) {
			t.Fatal("compression changed evidence bytes")
		}
		rawBytes += int64(len(raw))
		compressedBytes += int64(compressed.Len())
		maxRaw = max(maxRaw, len(raw))
		maxCompressed = max(maxCompressed, compressed.Len())
	}
	if len(fixture.devices) != 100 {
		t.Fatal("fixture changed represented device count")
	}
	report := struct {
		SchemaVersion           int    `json:"schema_version"`
		Kind                    string `json:"kind"`
		Collections             int    `json:"collections"`
		Devices                 int    `json:"devices"`
		Observations            int    `json:"observations"`
		RawBytes                int64  `json:"raw_bytes"`
		CompressedBytes         int64  `json:"compressed_bytes"`
		MaxRawBatchBytes        int    `json:"max_raw_batch_bytes"`
		MaxCompressedBatchBytes int    `json:"max_compressed_batch_bytes"`
		Codec                   string `json:"codec"`
	}{1, "codec-feasibility-not-storage-budget", rounds, 100, rounds * 100, rawBytes, compressedBytes, maxRaw, maxCompressed, fmt.Sprintf("gzip-level-%d; one 100-observation collection per independent batch", level)}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("compression-experiment-report: %s", raw)
}
