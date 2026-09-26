package adguard

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

var ErrObservationIngestion = errors.New("AdGuard Home observations could not be fully stored")
var ErrObservationAudit = errors.New("AdGuard Home collection audit could not be confirmed")

type CollectionResult struct {
	ScopeID         string
	QueryLogEnabled bool
	Read            int
	LimitReached    bool
	Inserted        int
	Deduplicated    int
	Skipped         ObservationStats
}

type collectorStore interface {
	GetNetworkScope(context.Context, string) (domain.NetworkScope, error)
	EnsureSensor(context.Context, domain.Sensor) error
	InsertAuditEvent(context.Context, domain.AuditEvent) error
}

// Collector is an explicit, one-shot, read-only external collection path. It
// never polls in the background or configures the resolver. A query is stored
// only if its client IP belongs to the selected, currently enrolled scope.
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

func (c *Collector) Collect(ctx context.Context, scopeID string) (result CollectionResult, operationErr error) {
	return c.collect(ctx, scopeID, nil)
}

// CollectReviewed rejects a changed scope or resolver before reading query history.
func (c *Collector) CollectReviewed(ctx context.Context, scopeID string, expectedEndpoint string, expectedBinding devicewatch.ScopeBinding) (CollectionResult, error) {
	if expectedEndpoint == "" || devicewatch.ValidateScopeBinding(expectedBinding) != nil {
		return CollectionResult{}, ErrObservationScope
	}
	return c.collect(ctx, scopeID, &collectionReview{endpoint: expectedEndpoint, binding: expectedBinding})
}

type collectionReview struct {
	endpoint string
	binding  devicewatch.ScopeBinding
}

func (c *Collector) collect(ctx context.Context, scopeID string, reviewed *collectionReview) (result CollectionResult, operationErr error) {
	scope, err := c.store.GetNetworkScope(ctx, scopeID)
	if err != nil || scope.RetiredAt != nil {
		return CollectionResult{}, ErrObservationScope
	}
	binding, err := devicewatch.ParseScopeBinding(scope)
	if err != nil {
		return CollectionResult{}, ErrObservationScope
	}
	if reviewed != nil && !reflect.DeepEqual(binding, reviewed.binding) {
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
	var snapshot Snapshot
	var endpoint string
	if reviewed == nil {
		snapshot, endpoint, err = c.connections.ReadSnapshot(ctx)
	} else {
		snapshot, endpoint, err = c.connections.ReadSnapshotBound(ctx, reviewed.endpoint)
	}
	if err != nil {
		return CollectionResult{}, err
	}
	latest, err := c.store.GetNetworkScope(ctx, scopeID)
	if err != nil || latest.RetiredAt != nil || !bytes.Equal(latest.Metadata, scope.Metadata) {
		return CollectionResult{}, ErrObservationScope
	}
	if _, err := devicewatch.ValidateCurrentScope(ctx, c.inspector, binding); err != nil {
		return CollectionResult{}, ErrObservationScope
	}
	digest := sha256.Sum256([]byte(scopeID + "\x00" + endpoint))
	sensorID := "sensor.adguard." + hex.EncodeToString(digest[:16])
	now := c.now().UTC()
	observations, stats, err := BuildObservations(snapshot, scopeID, sensorID, prefixes, now)
	if err != nil {
		return CollectionResult{}, err
	}
	result = CollectionResult{ScopeID: scopeID, QueryLogEnabled: snapshot.Status.QueryLogEnabled,
		Read: len(snapshot.Queries), LimitReached: len(snapshot.Queries) == MaxQueryLogEntries, Skipped: stats}
	if len(observations) == 0 {
		return result, nil
	}
	metadata := json.RawMessage(`{"schema_version":1,"capability":"adguard-home"}`)
	if err := c.store.EnsureSensor(ctx, domain.Sensor{ID: sensorID, ScopeID: scopeID, Kind: "adguard-home", Ownership: "external", RegisteredAt: now, Metadata: metadata}); err != nil {
		return CollectionResult{}, ErrObservationIngestion
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
		LimitReached  bool   `json:"limit_reached"`
	}{1, scopeID, phase, result.Read, result.Inserted, result.LimitReached})
	if err != nil {
		return err
	}
	return c.store.InsertAuditEvent(ctx, domain.AuditEvent{ID: "audit.adguard-collection." + hex.EncodeToString(random[:]),
		Kind: "adguard-collection", Actor: "local-os-user", OccurredAt: c.now().UTC(),
		SchemaVersion: 1, Payload: payload, Retention: domain.RetentionAudit})
}
