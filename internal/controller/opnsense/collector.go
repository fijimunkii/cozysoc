package opnsense

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

var (
	ErrObservationScope     = errors.New("OPNsense observation scope is invalid")
	ErrObservationIngestion = errors.New("OPNsense observations could not be fully stored")
	ErrObservationAudit     = errors.New("OPNsense collection audit could not be confirmed")
)

type CollectionResult struct {
	ScopeID          string
	Read             int
	IPv4Total        int
	IPv6Total        int
	IPv4Truncated    bool
	IPv6Truncated    bool
	Inserted         int
	Deduplicated     int
	SkippedOutside   int
	SkippedDuplicate int
}

type collectorStore interface {
	GetNetworkScope(context.Context, string) (domain.NetworkScope, error)
	EnsureSensor(context.Context, domain.Sensor) error
	InsertAuditEvent(context.Context, domain.AuditEvent) error
}

type Collector struct {
	connections *Connections
	store       collectorStore
	ingestor    *storage.Ingestor
	inspector   devicewatch.InterfaceInspector
	now         func() time.Time
}

func NewCollector(connections *Connections, store collectorStore, ingestor *storage.Ingestor, inspector devicewatch.InterfaceInspector) (*Collector, error) {
	if connections == nil || store == nil || ingestor == nil || inspector == nil {
		return nil, ErrObservationScope
	}
	return &Collector{connections: connections, store: store, ingestor: ingestor, inspector: inspector, now: time.Now}, nil
}

// CollectReviewed requires the endpoint and exact enrolled interface binding
// shown during the foreground decision. A changed origin or scope blocks the
// router read. There is no background collection path.
func (c *Collector) CollectReviewed(ctx context.Context, scopeID, expectedEndpoint string, expectedBinding devicewatch.ScopeBinding) (result CollectionResult, operationErr error) {
	if expectedEndpoint == "" || devicewatch.ValidateScopeBinding(expectedBinding) != nil {
		return CollectionResult{}, ErrObservationScope
	}
	scope, err := c.store.GetNetworkScope(ctx, scopeID)
	if err != nil || scope.RetiredAt != nil {
		return CollectionResult{}, ErrObservationScope
	}
	binding, err := devicewatch.ParseScopeBinding(scope)
	if err != nil || !reflect.DeepEqual(binding, expectedBinding) {
		return CollectionResult{}, ErrObservationScope
	}
	if _, err := devicewatch.ValidateCurrentScope(ctx, c.inspector, binding); err != nil {
		return CollectionResult{}, ErrObservationScope
	}
	prefixes := make([]netip.Prefix, 0, len(binding.Prefixes))
	for _, raw := range binding.Prefixes {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return CollectionResult{}, ErrObservationScope
		}
		prefixes = append(prefixes, prefix)
	}
	if err := c.recordAudit(ctx, scopeID, "requested", CollectionResult{}); err != nil {
		return CollectionResult{}, ErrObservationAudit
	}
	defer func() {
		phase := "completed"
		if operationErr != nil {
			phase = "failed"
		}
		auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.recordAudit(auditCtx, scopeID, phase, result); err != nil {
			operationErr = errors.Join(operationErr, ErrObservationAudit)
		}
	}()
	snapshot, err := c.connections.ReadSnapshotBound(ctx, expectedEndpoint)
	if err != nil {
		return CollectionResult{}, err
	}
	result = CollectionResult{ScopeID: scopeID, Read: len(snapshot.Neighbors), IPv4Total: snapshot.IPv4Total, IPv6Total: snapshot.IPv6Total,
		IPv4Truncated: snapshot.IPv4Truncated, IPv6Truncated: snapshot.IPv6Truncated}
	latest, err := c.store.GetNetworkScope(ctx, scopeID)
	if err != nil || latest.RetiredAt != nil || !bytes.Equal(latest.Metadata, scope.Metadata) {
		return result, ErrObservationScope
	}
	if _, err := devicewatch.ValidateCurrentScope(ctx, c.inspector, binding); err != nil {
		return result, ErrObservationScope
	}
	now := c.now().UTC()
	digest := sha256.Sum256([]byte(scopeID + "\x00" + expectedEndpoint))
	sensorID := "sensor.opnsense." + hex.EncodeToString(digest[:16])
	observations, stats, err := BuildObservations(snapshot, scopeID, sensorID, prefixes, now)
	if err != nil {
		return result, err
	}
	result.SkippedOutside, result.SkippedDuplicate = stats.OutsideScope, stats.Duplicate
	if len(observations) == 0 {
		return result, nil
	}
	metadata := json.RawMessage(`{"schema_version":1,"capability":"opnsense"}`)
	if err := c.store.EnsureSensor(ctx, domain.Sensor{ID: sensorID, ScopeID: scopeID, Kind: "opnsense", Ownership: "external", RegisteredAt: now, Metadata: metadata}); err != nil {
		return result, ErrObservationIngestion
	}
	for _, observation := range observations {
		receipt, err := c.ingestor.SubmitObservation(ctx, observation, nil)
		if err != nil {
			return result, ErrObservationIngestion
		}
		outcome, err := receipt.Wait(ctx)
		if err != nil {
			return result, ErrObservationIngestion
		}
		if outcome.Inserted {
			result.Inserted++
		} else {
			result.Deduplicated++
		}
	}
	return result, nil
}

func (c *Collector) recordAudit(ctx context.Context, scopeID, phase string, result CollectionResult) error {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	payload, err := json.Marshal(struct {
		SchemaVersion int    `json:"schema_version"`
		ScopeID       string `json:"scope_id"`
		Phase         string `json:"phase"`
		Read          int    `json:"read"`
		Inserted      int    `json:"inserted"`
		Truncated     bool   `json:"truncated"`
	}{1, scopeID, phase, result.Read, result.Inserted, result.IPv4Truncated || result.IPv6Truncated})
	if err != nil {
		return err
	}
	return c.store.InsertAuditEvent(ctx, domain.AuditEvent{ID: "audit.opnsense-collection." + hex.EncodeToString(random[:]),
		Kind: "opnsense-collection", Actor: "local-os-user", OccurredAt: c.now().UTC(),
		SchemaVersion: 1, Payload: payload, Retention: domain.RetentionAudit})
}
