package opnsense

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

const CapabilityID = "opnsense"

var (
	ErrAlreadyConnected = errors.New("OPNsense connection already exists")
	ErrNotConnected     = errors.New("OPNsense is not connected")
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
}

type Connection struct {
	Endpoint string
	Status   Status
}

// Connections serializes external-router connection transitions. Only a
// protected reference, the approved origin, and redacted audits are durable.
// It never grants Cozy SOC authority over router configuration.
type Connections struct {
	mu          sync.Mutex
	config      configurationStore
	audit       auditStore
	lifecycle   lifecycleState
	secretStore func() (secretstore.Store, error)
	probe       func(string, string, secretstore.Secret, []byte) (serviceProbe, error)
	now         func() time.Time
}

func NewConnections(config configurationStore, audit auditStore, lifecycle lifecycleState, secrets func() (secretstore.Store, error)) (*Connections, error) {
	if config == nil || audit == nil || lifecycle == nil || secrets == nil {
		return nil, errors.New("OPNsense connection dependencies are unavailable")
	}
	return &Connections{config: config, audit: audit, lifecycle: lifecycle, secretStore: secrets,
		probe: func(endpoint, key string, secret secretstore.Secret, trust []byte) (serviceProbe, error) {
			return NewClient(endpoint, key, secret, trust)
		}, now: time.Now}, nil
}

type protectedCredential struct {
	Endpoint string `json:"endpoint"`
	Key      string `json:"key"`
	Secret   string `json:"secret"`
	TrustPEM string `json:"trust_pem,omitempty"`
}

// Connect validates the chosen router and exact version before persisting
// local intent. A disabled reference is durable before writing Keychain, so
// failed cleanup can be retried after restart without enabling a connection.
func (c *Connections) Connect(ctx context.Context, endpoint, key string, secret secretstore.Secret, trustPEM []byte) (Connection, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.config.Capability(CapabilityID); exists {
		return Connection{}, ErrAlreadyConnected
	}
	if err := c.recordAudit(ctx, "requested"); err != nil {
		return Connection{}, err
	}
	client, err := c.probe(endpoint, key, secret, trustPEM)
	if err != nil {
		_ = c.recordAudit(context.Background(), "failed")
		return Connection{}, err
	}
	status, err := client.Probe(ctx)
	if err != nil {
		_ = c.recordAudit(context.Background(), "failed")
		return Connection{}, err
	}
	if err := ctx.Err(); err != nil {
		_ = c.recordAudit(context.Background(), "failed")
		return Connection{}, err
	}
	secrets, err := c.secretStore()
	if err != nil {
		_ = c.recordAudit(context.Background(), "failed")
		return Connection{}, err
	}
	ref, err := newCredentialReference()
	if err != nil {
		_ = c.recordAudit(context.Background(), "failed")
		return Connection{}, err
	}
	encoded, err := json.Marshal(protectedCredential{Endpoint: endpoint, Key: key, Secret: string(secret.Bytes()), TrustPEM: string(trustPEM)})
	if err != nil {
		_ = c.recordAudit(context.Background(), "failed")
		return Connection{}, err
	}
	configuration, err := connectionConfiguration(endpoint, ref, capability.DesiredDisabled)
	if err != nil {
		_ = c.recordAudit(context.Background(), "failed")
		return Connection{}, err
	}
	if _, _, err := c.config.ReplaceCapability(CapabilityID, &configuration); err != nil {
		_ = c.recordAudit(context.Background(), "failed")
		return Connection{}, err
	}
	cleanup := func() error {
		err := secrets.Delete(context.Background(), ref)
		if errors.Is(err, secretstore.ErrNotFound) {
			return nil
		}
		return err
	}
	if err := secrets.Put(ctx, ref, secretstore.NewSecret(encoded)); err != nil {
		return Connection{}, errors.Join(err, c.undoConnect(configuration, cleanup))
	}
	configuration.Desired = capability.DesiredEnabled
	if _, _, err := c.config.ReplaceCapability(CapabilityID, &configuration); err != nil {
		return Connection{}, errors.Join(err, c.undoConnect(configuration, cleanup))
	}
	if err := c.lifecycle.ApplyConfiguration(configuration); err != nil {
		return Connection{}, errors.Join(err, c.undoConnect(configuration, cleanup))
	}
	if err := c.recordAudit(ctx, "applied"); err != nil {
		return Connection{}, errors.Join(err, c.undoConnect(configuration, cleanup))
	}
	return Connection{Endpoint: endpoint, Status: status}, nil
}

func (c *Connections) undoConnect(configuration capability.Configuration, cleanup func() error) error {
	configuration.Desired = capability.DesiredDisabled
	_, _, disableErr := c.config.ReplaceCapability(CapabilityID, &configuration)
	var stateErr, secretErr, removeErr error
	if disableErr == nil {
		stateErr = c.lifecycle.ApplyConfiguration(configuration)
		if stateErr == nil {
			secretErr = cleanup()
			if secretErr == nil {
				_, _, removeErr = c.config.ReplaceCapability(CapabilityID, nil)
				if removeErr == nil {
					stateErr = c.lifecycle.RemoveConfiguration(CapabilityID)
				}
			}
		}
	}
	return errors.Join(disableErr, stateErr, secretErr, removeErr, c.recordAudit(context.Background(), "failed"))
}

// Current makes a fresh status-only read. A missing, locked, or denied secret
// fails this read instead of presenting saved intent as live router health.
func (c *Connections) Current(ctx context.Context) (Connection, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	configured, ok := c.config.Capability(CapabilityID)
	if !ok || configured.Desired != capability.DesiredEnabled {
		return Connection{}, ErrNotConnected
	}
	endpoint, ref, err := parseConnectionConfiguration(configured)
	if err != nil {
		return Connection{}, err
	}
	secrets, err := c.secretStore()
	if err != nil {
		return Connection{}, err
	}
	protected, err := secrets.Get(ctx, ref)
	if err != nil {
		return Connection{}, err
	}
	if protected.Len() == 0 || protected.Len() > 64<<10 {
		return Connection{}, ErrResponse
	}
	var credential protectedCredential
	if err := json.Unmarshal(protected.Bytes(), &credential); err != nil {
		return Connection{}, ErrResponse
	}
	if credential.Endpoint != endpoint {
		return Connection{}, ErrResponse
	}
	client, err := c.probe(endpoint, credential.Key, secretstore.NewSecret([]byte(credential.Secret)), []byte(credential.TrustPEM))
	if err != nil {
		return Connection{}, err
	}
	status, err := client.Probe(ctx)
	if err != nil {
		return Connection{}, err
	}
	return Connection{Endpoint: endpoint, Status: status}, nil
}

// Disconnect disables local intent before revoking the protected item. A
// failed Keychain deletion leaves disabled configuration for a safe retry.
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
	_, ref, err := parseConnectionConfiguration(configured)
	if err != nil {
		return err
	}
	secrets, err := c.secretStore()
	if err != nil {
		return err
	}
	if err := secrets.Delete(ctx, ref); err != nil && !errors.Is(err, secretstore.ErrNotFound) {
		return err
	}
	if _, _, err := c.config.ReplaceCapability(CapabilityID, nil); err != nil {
		return err
	}
	if err := c.lifecycle.RemoveConfiguration(CapabilityID); err != nil {
		return err
	}
	return c.recordAudit(ctx, "disconnect-applied")
}

func connectionConfiguration(endpoint string, ref secretstore.Reference, desired capability.DesiredState) (capability.Configuration, error) {
	endpointJSON, err := json.Marshal(endpoint)
	if err != nil {
		return capability.Configuration{}, err
	}
	refJSON, err := json.Marshal(ref.String())
	if err != nil {
		return capability.Configuration{}, err
	}
	return capability.Configuration{ID: CapabilityID, Ownership: capability.OwnershipExternal, Desired: desired,
		Values: map[string]json.RawMessage{"endpoint": endpointJSON, "credential_ref": refJSON}}, nil
}

func parseConnectionConfiguration(configuration capability.Configuration) (string, secretstore.Reference, error) {
	var endpoint, refString string
	if err := json.Unmarshal(configuration.Values["endpoint"], &endpoint); err != nil || endpoint == "" {
		return "", secretstore.Reference{}, ErrEndpoint
	}
	if err := json.Unmarshal(configuration.Values["credential_ref"], &refString); err != nil {
		return "", secretstore.Reference{}, ErrResponse
	}
	ref, err := secretstore.ParseReference(refString)
	if err != nil {
		return "", secretstore.Reference{}, ErrResponse
	}
	return endpoint, ref, nil
}

func newCredentialReference() (secretstore.Reference, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return secretstore.Reference{}, fmt.Errorf("generate OPNsense credential reference: %w", err)
	}
	return secretstore.ParseReference("opnsense/" + hex.EncodeToString(random[:]))
}

func (c *Connections) recordAudit(ctx context.Context, phase string) error {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"schema_version": 1, "capability_id": CapabilityID, "ownership": "external", "phase": phase})
	if err != nil {
		return err
	}
	return c.audit.InsertAuditEvent(ctx, domain.AuditEvent{ID: "audit.opnsense." + hex.EncodeToString(random[:]), Kind: "external-connection", Actor: "local-os-user", OccurredAt: c.now().UTC(), SchemaVersion: 1, Payload: payload, Retention: domain.RetentionAudit})
}
