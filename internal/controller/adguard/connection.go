package adguard

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
)

const CapabilityID = "adguard-home"

var (
	ErrAlreadyConnected  = errors.New("AdGuard Home connection already exists")
	ErrNotConnected      = errors.New("AdGuard Home is not connected")
	ErrNotRunning        = errors.New("AdGuard Home is not running")
	ErrConnectionChanged = errors.New("AdGuard Home connection changed before the approved read")
)

type configurationStore interface {
	Capability(string) (capability.Configuration, bool)
	ReplaceCapability(string, *capability.Configuration) (*capability.Configuration, bool, error)
}

type auditStore interface {
	InsertAuditEvent(context.Context, domain.AuditEvent) error
}

type lifecycleState interface {
	ApplyConfiguration(capability.Configuration) error
	RemoveConfiguration(string) error
}

type serviceProbe interface {
	Probe(context.Context) (Status, error)
	Read(context.Context) (Snapshot, error)
}

type Connection struct {
	Endpoint string
	Username string
	Status   Status
}

// Connections serializes external connection transitions within the controller.
// A connection never grants authority to change the external service.
type Connections struct {
	mu          sync.Mutex
	config      configurationStore
	audit       auditStore
	lifecycle   lifecycleState
	secretStore func() (secretstore.Store, error)
	probe       func(string, string, secretstore.Secret) (serviceProbe, error)
	now         func() time.Time
}

func NewConnections(config configurationStore, audit auditStore, lifecycle lifecycleState, secrets func() (secretstore.Store, error)) (*Connections, error) {
	if config == nil || audit == nil || lifecycle == nil || secrets == nil {
		return nil, errors.New("AdGuard Home connection dependencies are unavailable")
	}
	return &Connections{config: config, audit: audit, lifecycle: lifecycle, secretStore: secrets,
		probe: func(endpoint, username string, password secretstore.Secret) (serviceProbe, error) {
			return NewClient(endpoint, username, password)
		}, now: time.Now}, nil
}

// Connect validates the selected service before recording external ownership.
// Password is transient input; only an opaque Keychain reference is persisted.
func (c *Connections) Connect(ctx context.Context, endpoint, username string, password secretstore.Secret) (Connection, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.config.Capability(CapabilityID); exists {
		return Connection{}, ErrAlreadyConnected
	}
	client, err := c.probe(endpoint, username, password)
	if err != nil {
		return Connection{}, err
	}
	status, err := client.Probe(ctx)
	if err != nil {
		return Connection{}, err
	}
	if !status.Running {
		return Connection{}, ErrNotRunning
	}
	if err := ctx.Err(); err != nil {
		return Connection{}, err
	}
	if err := c.recordAudit(ctx, "requested"); err != nil {
		return Connection{}, err
	}
	var secrets secretstore.Store
	var ref secretstore.Reference
	if username != "" {
		secrets, err = c.secretStore()
		if err != nil {
			_ = c.recordAudit(context.Background(), "failed")
			return Connection{}, err
		}
		ref, err = newSecretReference()
		if err != nil {
			_ = c.recordAudit(context.Background(), "failed")
			return Connection{}, err
		}
	}
	cleanupSecret := func() error {
		if secrets == nil {
			return nil
		}
		err := secrets.Delete(context.Background(), ref)
		if errors.Is(err, secretstore.ErrNotFound) {
			return nil
		}
		return err
	}
	// Persist a disabled reference before touching Keychain. If a write or
	// cleanup fails, disconnect can revoke it after restart without exposing an
	// enabled connection or losing the only durable reference to the item.
	configuration, err := connectionConfiguration(endpoint, username, ref, capability.DesiredDisabled)
	if err != nil {
		_ = c.recordAudit(context.Background(), "failed")
		return Connection{}, err
	}
	if _, _, err := c.config.ReplaceCapability(CapabilityID, &configuration); err != nil {
		_ = c.recordAudit(context.Background(), "failed")
		return Connection{}, err
	}
	if secrets != nil {
		if err := secrets.Put(ctx, ref, password); err != nil {
			return Connection{}, errors.Join(err, c.undoConnect(configuration, cleanupSecret))
		}
	}
	configuration.Desired = capability.DesiredEnabled
	if _, _, err := c.config.ReplaceCapability(CapabilityID, &configuration); err != nil {
		return Connection{}, errors.Join(err, c.undoConnect(configuration, cleanupSecret))
	}
	if err := c.lifecycle.ApplyConfiguration(configuration); err != nil {
		return Connection{}, errors.Join(err, c.undoConnect(configuration, cleanupSecret))
	}
	if err := c.recordAudit(ctx, "applied"); err != nil {
		return Connection{}, errors.Join(err, c.undoConnect(configuration, cleanupSecret))
	}
	return Connection{Endpoint: endpoint, Username: username, Status: status}, nil
}

func (c *Connections) undoConnect(configuration capability.Configuration, cleanupSecret func() error) error {
	configuration.Desired = capability.DesiredDisabled
	_, _, disableErr := c.config.ReplaceCapability(CapabilityID, &configuration)
	var stateErr, secretErr, removeErr error
	if disableErr == nil {
		stateErr = c.lifecycle.ApplyConfiguration(configuration)
		if stateErr == nil {
			secretErr = cleanupSecret()
			if secretErr == nil {
				_, _, removeErr = c.config.ReplaceCapability(CapabilityID, nil)
				if removeErr == nil {
					stateErr = c.lifecycle.RemoveConfiguration(CapabilityID)
				}
			}
		}
	}
	auditErr := c.recordAudit(context.Background(), "failed")
	return errors.Join(disableErr, stateErr, secretErr, removeErr, auditErr)
}

// Current makes a fresh status-only read. A missing, locked, or denied secret
// fails this read instead of presenting historical connection state as healthy.
func (c *Connections) Current(ctx context.Context) (Connection, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	client, endpoint, username, err := c.configuredClient(ctx)
	if err != nil {
		return Connection{}, err
	}
	status, err := client.Probe(ctx)
	if err != nil {
		return Connection{}, err
	}
	return Connection{Endpoint: endpoint, Username: username, Status: status}, nil
}

// ReadSnapshot is available only to an explicit collection request. Normal
// connection and status paths continue to use the status-only Probe method.
func (c *Connections) ReadSnapshot(ctx context.Context) (Snapshot, string, error) {
	return c.readSnapshot(ctx, "")
}

// ReadSnapshotBound compares the reviewed origin under the connection lock,
// before any request for private query history can reach the external service.
func (c *Connections) ReadSnapshotBound(ctx context.Context, expectedEndpoint string) (Snapshot, string, error) {
	if expectedEndpoint == "" {
		return Snapshot{}, "", ErrConnectionChanged
	}
	return c.readSnapshot(ctx, expectedEndpoint)
}

func (c *Connections) readSnapshot(ctx context.Context, expectedEndpoint string) (Snapshot, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	client, endpoint, _, err := c.configuredClient(ctx)
	if err != nil {
		return Snapshot{}, "", err
	}
	if expectedEndpoint != "" && endpoint != expectedEndpoint {
		return Snapshot{}, "", ErrConnectionChanged
	}
	snapshot, err := client.Read(ctx)
	if err != nil {
		return Snapshot{}, "", err
	}
	return snapshot, endpoint, nil
}

func (c *Connections) configuredClient(ctx context.Context) (serviceProbe, string, string, error) {
	configured, ok := c.config.Capability(CapabilityID)
	if !ok || configured.Desired != capability.DesiredEnabled {
		return nil, "", "", ErrNotConnected
	}
	endpoint, username, ref, err := parseConnectionConfiguration(configured)
	if err != nil {
		return nil, "", "", err
	}
	var password secretstore.Secret
	if ref.String() != "" {
		secrets, err := c.secretStore()
		if err != nil {
			return nil, "", "", err
		}
		password, err = secrets.Get(ctx, ref)
		if err != nil {
			return nil, "", "", err
		}
	}
	client, err := c.probe(endpoint, username, password)
	if err != nil {
		return nil, "", "", err
	}
	return client, endpoint, username, nil
}

// Disconnect disables local intent before deleting the Keychain item. A failed
// deletion leaves disabled configuration with its reference for a safe retry.
func (c *Connections) Disconnect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	configured, ok := c.config.Capability(CapabilityID)
	if !ok {
		return nil
	}
	if err := c.recordAudit(ctx, "disconnect-requested"); err != nil {
		return err
	}
	if configured.Desired != capability.DesiredDisabled {
		configured.Desired = capability.DesiredDisabled
		if _, _, err := c.config.ReplaceCapability(CapabilityID, &configured); err != nil {
			return err
		}
		if err := c.lifecycle.ApplyConfiguration(configured); err != nil {
			return err
		}
	}
	_, _, ref, err := parseConnectionConfiguration(configured)
	if err != nil {
		return err
	}
	if ref.String() != "" {
		secrets, err := c.secretStore()
		if err != nil {
			return err
		}
		if err := secrets.Delete(ctx, ref); err != nil && !errors.Is(err, secretstore.ErrNotFound) {
			return err
		}
	}
	if _, _, err := c.config.ReplaceCapability(CapabilityID, nil); err != nil {
		return err
	}
	if err := c.lifecycle.RemoveConfiguration(CapabilityID); err != nil {
		return err
	}
	return c.recordAudit(ctx, "disconnect-applied")
}

func connectionConfiguration(endpoint, username string, ref secretstore.Reference, desired capability.DesiredState) (capability.Configuration, error) {
	values := map[string]json.RawMessage{}
	for key, value := range map[string]string{"endpoint": endpoint, "username": username} {
		if key == "username" && value == "" {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return capability.Configuration{}, err
		}
		values[key] = encoded
	}
	if ref.String() != "" {
		encoded, err := json.Marshal(ref.String())
		if err != nil {
			return capability.Configuration{}, err
		}
		values["credential_ref"] = encoded
	}
	return capability.Configuration{ID: CapabilityID, Ownership: capability.OwnershipExternal, Desired: desired, Values: values}, nil
}

func parseConnectionConfiguration(configuration capability.Configuration) (string, string, secretstore.Reference, error) {
	var endpoint, username, refString string
	if err := json.Unmarshal(configuration.Values["endpoint"], &endpoint); err != nil {
		return "", "", secretstore.Reference{}, ErrEndpoint
	}
	if raw, ok := configuration.Values["username"]; ok {
		if err := json.Unmarshal(raw, &username); err != nil {
			return "", "", secretstore.Reference{}, ErrResponse
		}
	}
	if raw, ok := configuration.Values["credential_ref"]; ok {
		if err := json.Unmarshal(raw, &refString); err != nil {
			return "", "", secretstore.Reference{}, ErrResponse
		}
	}
	if (username == "") != (refString == "") {
		return "", "", secretstore.Reference{}, ErrResponse
	}
	if refString == "" {
		return endpoint, username, secretstore.Reference{}, nil
	}
	ref, err := secretstore.ParseReference(refString)
	if err != nil {
		return "", "", secretstore.Reference{}, ErrResponse
	}
	return endpoint, username, ref, nil
}

func newSecretReference() (secretstore.Reference, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return secretstore.Reference{}, fmt.Errorf("generate credential reference: %w", err)
	}
	return secretstore.ParseReference("adguard/" + hex.EncodeToString(random[:]))
}

func (c *Connections) recordAudit(ctx context.Context, phase string) error {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	encoded, err := json.Marshal(map[string]any{"schema_version": 1, "capability_id": CapabilityID, "ownership": "external", "phase": phase})
	if err != nil {
		return err
	}
	return c.audit.InsertAuditEvent(ctx, domain.AuditEvent{ID: "audit.adguard." + hex.EncodeToString(random[:]), Kind: "external-connection", Actor: "local-os-user", OccurredAt: c.now().UTC(), SchemaVersion: 1, Payload: encoded, Retention: domain.RetentionAudit})
}
