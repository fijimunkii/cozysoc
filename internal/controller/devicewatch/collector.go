package devicewatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

const CapabilityID = "device-watch"

const observationBucket = time.Minute

type EvidenceSink interface {
	PutObservation(context.Context, domain.Observation) (bool, error)
	PutCoverageSample(context.Context, domain.CoverageSample) error
}

type StorageSink struct {
	Ingestor *storage.Ingestor
}

func (s StorageSink) PutObservation(ctx context.Context, observation domain.Observation) (bool, error) {
	if s.Ingestor == nil {
		return false, fmt.Errorf("device watch ingestion is unavailable")
	}
	receipt, err := s.Ingestor.SubmitObservation(ctx, observation, nil)
	if err != nil {
		return false, err
	}
	result, err := receipt.Wait(ctx)
	if err != nil {
		return false, err
	}
	return result.Inserted, nil
}

func (s StorageSink) PutCoverageSample(ctx context.Context, sample domain.CoverageSample) error {
	if s.Ingestor == nil {
		return fmt.Errorf("device watch ingestion is unavailable")
	}
	receipt, err := s.Ingestor.SubmitCoverageSample(ctx, sample)
	if err != nil {
		return err
	}
	_, err = receipt.Wait(ctx)
	return err
}

type Collector struct {
	snapshotter Snapshotter
	inspector   InterfaceInspector
	sink        EvidenceSink
	now         func() time.Time
}

type CollectionResult struct {
	CapturedAt   time.Time
	Visible      int
	Inserted     int
	Deduplicated int
	Sources      []SourceStatus
}

func NewCollector(snapshotter Snapshotter, inspector InterfaceInspector, sink EvidenceSink) (*Collector, error) {
	if snapshotter == nil {
		return nil, fmt.Errorf("device watch snapshotter is required")
	}
	if inspector == nil {
		return nil, fmt.Errorf("device watch interface inspector is required")
	}
	if sink == nil {
		return nil, fmt.Errorf("device watch evidence sink is required")
	}
	return &Collector{snapshotter: snapshotter, inspector: inspector, sink: sink, now: time.Now}, nil
}

func (c *Collector) CollectOnce(ctx context.Context, scopeID, sensorID string, binding ScopeBinding) (CollectionResult, error) {
	if c == nil {
		return CollectionResult{}, fmt.Errorf("device watch collector is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return CollectionResult{}, err
	}
	if _, err := ValidateCurrentScope(ctx, c.inspector, binding); err != nil {
		return CollectionResult{}, err
	}

	snapshot, err := c.snapshotter.Snapshot(ctx, binding.InterfaceName)
	if err != nil {
		capturedAt := c.now().UTC()
		coverageErr := c.putCoverage(ctx, scopeID, sensorID, binding, Snapshot{
			CapturedAt:    capturedAt,
			InterfaceName: binding.InterfaceName,
			Sources: []SourceStatus{
				{Method: MethodARPCache},
				{Method: MethodNDPCache},
			},
		}, 0, 0, 0, "unavailable")
		if coverageErr != nil {
			return CollectionResult{}, errors.Join(err, coverageErr)
		}
		return CollectionResult{}, err
	}
	if snapshot.InterfaceName != binding.InterfaceName {
		return CollectionResult{}, fmt.Errorf("%w: snapshot interface changed", ErrScopeMismatch)
	}
	if snapshot.CapturedAt.IsZero() {
		snapshot.CapturedAt = c.now().UTC()
	}
	if len(snapshot.Neighbors) > MaxNeighborEntries {
		return CollectionResult{}, ErrSnapshotTooLarge
	}

	neighbors := make([]Neighbor, 0, len(snapshot.Neighbors))
	for _, neighbor := range snapshot.Neighbors {
		if neighbor.InterfaceName != binding.InterfaceName || !AddressInScope(binding, neighbor.Address) || !usableHardwareAddr(neighbor.HardwareAddr) {
			continue
		}
		neighbors = append(neighbors, neighbor)
	}
	neighbors = deduplicateNeighbors(neighbors)

	result := CollectionResult{
		CapturedAt: snapshot.CapturedAt.UTC(),
		Visible:    len(neighbors),
		Sources:    append([]SourceStatus(nil), snapshot.Sources...),
	}
	for _, neighbor := range neighbors {
		observation, buildErr := buildNeighborObservation(scopeID, sensorID, snapshot.CapturedAt, neighbor)
		if buildErr != nil {
			return result, buildErr
		}
		inserted, ingestErr := c.sink.PutObservation(ctx, observation)
		if ingestErr != nil {
			return result, ingestErr
		}
		if inserted {
			result.Inserted++
		} else {
			result.Deduplicated++
		}
	}

	status := "partial"
	if !hasAvailableSource(snapshot.Sources) {
		status = "unavailable"
	}
	if err := c.putCoverage(ctx, scopeID, sensorID, binding, snapshot, result.Visible, result.Inserted, result.Deduplicated, status); err != nil {
		return result, err
	}
	return result, nil
}

func buildNeighborObservation(scopeID, sensorID string, capturedAt time.Time, neighbor Neighbor) (domain.Observation, error) {
	family := "ipv6"
	if neighbor.Address.Is4() {
		family = "ipv4"
	}
	capturedAt = capturedAt.UTC()
	bucket := capturedAt.Truncate(observationBucket)
	mac := strings.ToLower(neighbor.HardwareAddr.String())
	sourceKey := stableDigest(
		"neighbor-v1",
		scopeID,
		sensorID,
		neighbor.InterfaceName,
		string(neighbor.Method),
		neighbor.Address.String(),
		mac,
		fmt.Sprintf("%d", bucket.Unix()),
	)
	payload, err := json.Marshal(map[string]any{
		"schema_version":   1,
		"address":          neighbor.Address.String(),
		"hardware_address": mac,
		"interface":        neighbor.InterfaceName,
		"family":           family,
		"method":           neighbor.Method,
		"state":            neighbor.State,
	})
	if err != nil {
		return domain.Observation{}, fmt.Errorf("encode device watch observation: %w", err)
	}
	observation := domain.Observation{
		ID:            "obs.dw." + sourceKey,
		ScopeID:       scopeID,
		SensorID:      sensorID,
		Kind:          "device-neighbor-seen",
		SourceStream:  "device-watch-neighbors",
		SourceKey:     sourceKey,
		SourceEventID: sourceKey,
		SourceTime:    &capturedAt,
		IngestedAt:    capturedAt,
		SchemaVersion: 1,
		Attribution:   "device-watch:" + string(neighbor.Method),
		Payload:       payload,
		Retention:     domain.RetentionStandard,
	}
	if err := domain.ValidateObservation(observation); err != nil {
		return domain.Observation{}, err
	}
	return observation, nil
}

func (c *Collector) putCoverage(ctx context.Context, scopeID, sensorID string, binding ScopeBinding, snapshot Snapshot, visible, inserted, deduplicated int, status string) error {
	sources := append([]SourceStatus(nil), snapshot.Sources...)
	sort.Slice(sources, func(i, j int) bool { return sources[i].Method < sources[j].Method })
	available := make([]map[string]any, 0, len(sources))
	for _, source := range sources {
		available = append(available, map[string]any{
			"method":    source.Method,
			"available": source.Available,
		})
	}
	evidence, err := json.Marshal(map[string]any{
		"schema_version":                1,
		"interface":                     binding.InterfaceName,
		"sources":                       available,
		"neighbors_in_scope":            visible,
		"observations_inserted":         inserted,
		"observations_deduplicated":     deduplicated,
		"whole_network_traffic_visible": false,
		"limitations": []string{
			"passive neighbor caches include only peers the host has recently resolved on the local link",
			"client isolation, other VLANs, and devices behind other observation points may be absent",
			"a successful neighbor snapshot does not provide whole-network traffic visibility",
		},
	})
	if err != nil {
		return fmt.Errorf("encode device watch coverage: %w", err)
	}
	capturedAt := snapshot.CapturedAt.UTC()
	if capturedAt.IsZero() {
		capturedAt = c.now().UTC()
	}
	coverageIDTime := c.now().UTC()
	coverage := domain.CoverageSample{
		ID:            "coverage.dw." + stableDigest("coverage-v1", scopeID, sensorID, binding.InterfaceName, fmt.Sprintf("%d", capturedAt.UnixNano()), fmt.Sprintf("%d", coverageIDTime.UnixNano())),
		ScopeID:       scopeID,
		SensorID:      sensorID,
		CapabilityID:  CapabilityID,
		Status:        status,
		StartedAt:     capturedAt,
		EndedAt:       capturedAt,
		SchemaVersion: 1,
		Evidence:      evidence,
		Retention:     domain.RetentionShort,
	}
	if err := domain.ValidateCoverageSample(coverage); err != nil {
		return err
	}
	return c.sink.PutCoverageSample(ctx, coverage)
}

func stableDigest(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)[:16])
}

func hasAvailableSource(sources []SourceStatus) bool {
	for _, source := range sources {
		if source.Available {
			return true
		}
	}
	return false
}

func usableHardwareAddr(address net.HardwareAddr) bool {
	if len(address) != 6 || address[0]&1 != 0 {
		return false
	}
	allZero := true
	for _, value := range address {
		if value != 0 {
			allZero = false
			break
		}
	}
	return !allZero
}
