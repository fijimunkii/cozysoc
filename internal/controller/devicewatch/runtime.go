package devicewatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

const (
	defaultCollectionInterval = time.Minute
	collectionTimeout         = 20 * time.Second
)

var (
	ErrRuntimeAlreadyStarted = errors.New("device watch runtime is already started")
	ErrPlatformUnsupported   = errors.New("device watch passive runtime is unsupported on this platform")
)

type RuntimeState struct {
	ScopeID          string    `json:"scope_id,omitempty"`
	SensorID         string    `json:"sensor_id,omitempty"`
	StartedAt        time.Time `json:"started_at,omitempty"`
	LastAttemptAt    time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessfulAt time.Time `json:"last_successful_at,omitempty"`
	LastVisible      int       `json:"last_visible"`
	LastErrorClass   string    `json:"last_error_class,omitempty"`
}

type Runtime struct {
	store       *storage.Store
	ingestor    *storage.Ingestor
	logger      *slog.Logger
	inspector   InterfaceInspector
	snapshotter Snapshotter
	interval    time.Duration
	now         func() time.Time

	mu      sync.RWMutex
	started bool
	state   RuntimeState
}

func NewRuntime(store *storage.Store, ingestor *storage.Ingestor, logger *slog.Logger) (*Runtime, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("%w: %s", ErrPlatformUnsupported, runtime.GOOS)
	}
	return newRuntime(store, ingestor, logger, NewSystemInterfaceInspector(), NewSystemSnapshotter(), defaultCollectionInterval)
}

func newRuntime(store *storage.Store, ingestor *storage.Ingestor, logger *slog.Logger, inspector InterfaceInspector, snapshotter Snapshotter, interval time.Duration) (*Runtime, error) {
	if store == nil || ingestor == nil {
		return nil, fmt.Errorf("device watch runtime requires storage and ingestion")
	}
	if inspector == nil || snapshotter == nil {
		return nil, fmt.Errorf("device watch runtime requires interface and snapshot sources")
	}
	if interval <= 0 || interval < 10*time.Second || interval > 10*time.Minute {
		return nil, fmt.Errorf("device watch collection interval is outside safe bounds")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Runtime{
		store:       store,
		ingestor:    ingestor,
		logger:      logger,
		inspector:   inspector,
		snapshotter: snapshotter,
		interval:    interval,
		now:         time.Now,
	}, nil
}

func EnabledScopeID(configurations []capability.Configuration) (string, bool, error) {
	for _, configuration := range configurations {
		if configuration.ID != CapabilityID {
			continue
		}
		if configuration.Desired != capability.DesiredEnabled {
			return "", false, nil
		}
		raw, ok := configuration.Values["network_scope_id"]
		if !ok {
			return "", false, fmt.Errorf("enabled device watch has no network_scope_id")
		}
		var scopeID string
		if err := json.Unmarshal(raw, &scopeID); err != nil || scopeID == "" {
			return "", false, fmt.Errorf("enabled device watch has invalid network_scope_id")
		}
		return scopeID, true, nil
	}
	return "", false, nil
}

func (r *Runtime) Start(ctx context.Context, scopeID string) error {
	if r == nil {
		return fmt.Errorf("device watch runtime is unavailable")
	}
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return ErrRuntimeAlreadyStarted
	}
	r.mu.Unlock()

	scope, err := r.store.GetNetworkScope(ctx, scopeID)
	if err != nil {
		return err
	}
	if scope.RetiredAt != nil && !scope.RetiredAt.After(r.now().UTC()) {
		return fmt.Errorf("device watch network scope %q is retired", scopeID)
	}
	binding, err := ParseScopeBinding(scope)
	if err != nil {
		return err
	}
	if _, err := ValidateCurrentScope(ctx, r.inspector, binding); err != nil {
		return err
	}

	sensorID := "sensor.dw." + stableDigest("sensor-v1", scopeID, binding.InterfaceName, fmt.Sprintf("%d", binding.InterfaceIndex))
	metadata, err := json.Marshal(map[string]any{
		"schema_version":  1,
		"capability_id":   CapabilityID,
		"interface":       binding.InterfaceName,
		"interface_index": binding.InterfaceIndex,
	})
	if err != nil {
		return fmt.Errorf("encode device watch sensor metadata: %w", err)
	}
	startedAt := r.now().UTC()
	if err := r.store.EnsureSensor(ctx, domain.Sensor{
		ID:           sensorID,
		ScopeID:      scopeID,
		Kind:         "desktop-neighbor-cache",
		Ownership:    "builtin",
		RegisteredAt: startedAt,
		Metadata:     metadata,
	}); err != nil {
		return err
	}

	reconciler, err := NewReconciler(r.store)
	if err != nil {
		return err
	}
	collector, err := NewCollector(r.snapshotter, r.inspector, ReconcilingSink{
		Evidence:   StorageSink{Ingestor: r.ingestor},
		Reconciler: reconciler,
	})
	if err != nil {
		return err
	}

	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return ErrRuntimeAlreadyStarted
	}
	r.started = true
	r.state = RuntimeState{ScopeID: scopeID, SensorID: sensorID, StartedAt: startedAt}
	r.mu.Unlock()

	go r.run(ctx, collector, scopeID, sensorID, binding)
	return nil
}

func (r *Runtime) State() RuntimeState {
	if r == nil {
		return RuntimeState{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

func (r *Runtime) run(ctx context.Context, collector *Collector, scopeID, sensorID string, binding ScopeBinding) {
	r.collect(ctx, collector, scopeID, sensorID, binding)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.collect(ctx, collector, scopeID, sensorID, binding)
		}
	}
}

func (r *Runtime) collect(parent context.Context, collector *Collector, scopeID, sensorID string, binding ScopeBinding) {
	attemptAt := r.now().UTC()
	ctx, cancel := context.WithTimeout(parent, collectionTimeout)
	defer cancel()
	result, err := collector.CollectOnce(ctx, scopeID, sensorID, binding)

	r.mu.Lock()
	r.state.LastAttemptAt = attemptAt
	if err == nil {
		r.state.LastSuccessfulAt = result.CapturedAt.UTC()
		r.state.LastVisible = result.Visible
		r.state.LastErrorClass = ""
	} else {
		r.state.LastErrorClass = runtimeErrorClass(err)
	}
	r.mu.Unlock()

	if err != nil {
		r.logger.Warn("device_watch_collection_failed", "reason", runtimeErrorClass(err))
		return
	}
	r.logger.Debug("device_watch_collection_complete",
		"visible", result.Visible,
		"inserted", result.Inserted,
		"deduplicated", result.Deduplicated,
	)
}

func runtimeErrorClass(err error) string {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	case errors.Is(err, ErrScopeMismatch):
		return "scope-mismatch"
	case errors.Is(err, ErrUnsupportedInterface):
		return "unsupported-interface"
	case errors.Is(err, ErrSnapshotUnavailable):
		return "source-unavailable"
	case errors.Is(err, ErrSnapshotTooLarge):
		return "source-oversized"
	default:
		return "collection-error"
	}
}
