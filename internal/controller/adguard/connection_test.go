package adguard

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/config"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
)

type memoryConfig struct {
	current     *capability.Configuration
	failReplace bool
}

func (m *memoryConfig) Capability(id string) (capability.Configuration, bool) {
	if m.current == nil || id != CapabilityID {
		return capability.Configuration{}, false
	}
	return *m.current, true
}

func (m *memoryConfig) ReplaceCapability(id string, next *capability.Configuration) (*capability.Configuration, bool, error) {
	if m.failReplace {
		return nil, false, errors.New("synthetic config failure")
	}
	var previous *capability.Configuration
	if m.current != nil {
		copyValue := *m.current
		previous = &copyValue
	}
	if next == nil {
		m.current = nil
	} else {
		copyValue := *next
		m.current = &copyValue
	}
	return previous, true, nil
}

type memoryLifecycle struct{ desired capability.DesiredState }

func (m *memoryLifecycle) ApplyConfiguration(configuration capability.Configuration) error {
	m.desired = configuration.Desired
	return nil
}
func (m *memoryLifecycle) RemoveConfiguration(string) error {
	m.desired = capability.DesiredDisabled
	return nil
}

type memoryAudit struct {
	phases    []string
	failPhase string
}

func (m *memoryAudit) InsertAuditEvent(_ context.Context, event domain.AuditEvent) error {
	if err := domain.ValidateAuditEvent(event); err != nil {
		return err
	}
	var payload struct {
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	if payload.Phase == m.failPhase {
		return errors.New("synthetic audit failure")
	}
	m.phases = append(m.phases, payload.Phase)
	return nil
}

type memorySecrets struct {
	values       map[string]secretstore.Secret
	putErr       error
	deleteErr    error
	beforeDelete func()
}

func (m *memorySecrets) Put(_ context.Context, ref secretstore.Reference, value secretstore.Secret) error {
	if m.values == nil {
		m.values = map[string]secretstore.Secret{}
	}
	m.values[ref.String()] = secretstore.NewSecret(value.Bytes())
	return m.putErr
}
func (m *memorySecrets) Get(_ context.Context, ref secretstore.Reference) (secretstore.Secret, error) {
	v, ok := m.values[ref.String()]
	if !ok {
		return secretstore.Secret{}, secretstore.ErrNotFound
	}
	return v, nil
}
func (m *memorySecrets) Delete(_ context.Context, ref secretstore.Reference) error {
	if m.beforeDelete != nil {
		m.beforeDelete()
	}
	if m.deleteErr != nil {
		return m.deleteErr
	}
	if _, ok := m.values[ref.String()]; !ok {
		return secretstore.ErrNotFound
	}
	delete(m.values, ref.String())
	return nil
}
func (m *memorySecrets) Backend() secretstore.BackendInfo {
	return secretstore.BackendInfo{Kind: "test", Persistent: true}
}

type fixedProbe struct {
	status Status
	err    error
}

func (p fixedProbe) Probe(context.Context) (Status, error)  { return p.status, p.err }
func (p fixedProbe) Read(context.Context) (Snapshot, error) { return Snapshot{Status: p.status}, p.err }

func newFixtureConnections(t *testing.T) (*Connections, *memoryConfig, *memoryAudit, *memoryLifecycle, *memorySecrets) {
	t.Helper()
	config := &memoryConfig{}
	audit := &memoryAudit{}
	lifecycle := &memoryLifecycle{}
	secrets := &memorySecrets{}
	c, err := NewConnections(config, audit, lifecycle, func() (secretstore.Store, error) { return secrets, nil })
	if err != nil {
		t.Fatal(err)
	}
	c.probe = func(_, _ string, _ secretstore.Secret) (serviceProbe, error) {
		return fixedProbe{status: Status{Version: SupportedVersion, Running: true, ProtectionEnabled: true, FilteringEnabled: true, QueryLogEnabled: true}}, nil
	}
	c.now = func() time.Time { return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC) }
	return c, config, audit, lifecycle, secrets
}

func TestConnectionPersistsOnlyReferenceAndDisconnectsWithoutExternalMutation(t *testing.T) {
	c, config, audit, lifecycle, secrets := newFixtureConnections(t)
	password := secretstore.NewSecret([]byte("private-password"))
	connection, err := c.Connect(context.Background(), "https://192.0.2.5:3000", "reader", password)
	if err != nil {
		t.Fatal(err)
	}
	if connection.Endpoint != "https://192.0.2.5:3000" || !connection.Status.Running || config.current == nil || config.current.Ownership != capability.OwnershipExternal || lifecycle.desired != capability.DesiredEnabled {
		t.Fatalf("connection not established: %+v %+v", connection, config.current)
	}
	encoded, _ := json.Marshal(config.current)
	if strings.Contains(string(encoded), "private-password") || !strings.Contains(string(encoded), "credential_ref") || len(secrets.values) != 1 {
		t.Fatalf("secret leaked or missing: %s, %d secrets", encoded, len(secrets.values))
	}
	if strings.Join(audit.phases, ",") != "requested,applied" {
		t.Fatalf("missing connection audit: %v", audit.phases)
	}
	current, err := c.Current(context.Background())
	if err != nil || current.Endpoint != connection.Endpoint {
		t.Fatalf("current connection: %+v, %v", current, err)
	}
	secrets.beforeDelete = func() {
		if config.current == nil || config.current.Desired != capability.DesiredDisabled || lifecycle.desired != capability.DesiredDisabled {
			t.Error("secret deletion ran before disable committed")
		}
	}
	if err := c.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if config.current != nil || len(secrets.values) != 0 || strings.Join(audit.phases, ",") != "requested,applied,disconnect-requested,disconnect-applied" {
		t.Fatalf("disconnect left state: %+v, %v, %v", config.current, secrets.values, audit.phases)
	}
}

func TestConnectAuditFailureRemovesNewCredential(t *testing.T) {
	c, config, audit, _, secrets := newFixtureConnections(t)
	audit.failPhase = "requested"
	if _, err := c.Connect(context.Background(), "https://192.0.2.5", "reader", secretstore.NewSecret([]byte("secret"))); err == nil {
		t.Fatal("audit failure accepted")
	}
	if config.current != nil || len(secrets.values) != 0 {
		t.Fatalf("audit failure left connection or credential: %+v, %v", config.current, secrets.values)
	}
}

func TestConnectConfigFailureDoesNotRetainCredential(t *testing.T) {
	c, config, audit, _, secrets := newFixtureConnections(t)
	config.failReplace = true
	if _, err := c.Connect(context.Background(), "https://192.0.2.5", "reader", secretstore.NewSecret([]byte("secret"))); err == nil {
		t.Fatal("config failure accepted")
	}
	if config.current != nil || len(secrets.values) != 0 || strings.Join(audit.phases, ",") != "requested,failed" {
		t.Fatalf("config failure left state: %+v, %v, %v", config.current, secrets.values, audit.phases)
	}
}

func TestDisconnectDeletionFailureLeavesDisabledRetryableState(t *testing.T) {
	c, config, _, lifecycle, secrets := newFixtureConnections(t)
	if _, err := c.Connect(context.Background(), "https://192.0.2.5", "reader", secretstore.NewSecret([]byte("secret"))); err != nil {
		t.Fatal(err)
	}
	secrets.deleteErr = secretstore.ErrLocked
	if err := c.Disconnect(context.Background()); !errors.Is(err, secretstore.ErrLocked) {
		t.Fatalf("got %v", err)
	}
	if config.current == nil || config.current.Desired != capability.DesiredDisabled || lifecycle.desired != capability.DesiredDisabled {
		t.Fatalf("failed deletion left enabled intent: %+v", config.current)
	}
	if _, err := c.Current(context.Background()); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("disabled connection was readable: %v", err)
	}
	secrets.deleteErr = nil
	if err := c.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if config.current != nil || len(secrets.values) != 0 {
		t.Fatalf("retry did not clean up: %+v, %v", config.current, secrets.values)
	}
}

func TestFailedConnectRetainsDisabledReferenceUntilCredentialCanBeRevoked(t *testing.T) {
	c, config, audit, lifecycle, secrets := newFixtureConnections(t)
	audit.failPhase = "applied"
	secrets.deleteErr = secretstore.ErrLocked
	if _, err := c.Connect(context.Background(), "https://192.0.2.5", "reader", secretstore.NewSecret([]byte("secret"))); err == nil {
		t.Fatal("audit failure accepted")
	}
	if config.current == nil || config.current.Desired != capability.DesiredDisabled || lifecycle.desired != capability.DesiredDisabled || len(secrets.values) != 1 {
		t.Fatalf("failed connection was not safely recoverable: %+v, %v", config.current, secrets.values)
	}
	secrets.deleteErr = nil
	if err := c.Disconnect(context.Background()); err != nil || config.current != nil || len(secrets.values) != 0 {
		t.Fatalf("failed connection could not be cleaned up: %v, %+v", err, config.current)
	}
}

func TestPartialKeychainPutCanBeRevokedFromDisabledIntent(t *testing.T) {
	c, config, _, _, secrets := newFixtureConnections(t)
	secrets.putErr = secretstore.ErrUnavailable
	secrets.deleteErr = secretstore.ErrLocked
	if _, err := c.Connect(context.Background(), "https://192.0.2.5", "reader", secretstore.NewSecret([]byte("secret"))); !errors.Is(err, secretstore.ErrUnavailable) {
		t.Fatal(err)
	}
	if config.current == nil || config.current.Desired != capability.DesiredDisabled || len(secrets.values) != 1 {
		t.Fatalf("partial credential write lost recovery state: %+v, %v", config.current, secrets.values)
	}
	secrets.deleteErr = nil
	if err := c.Disconnect(context.Background()); err != nil || config.current != nil || len(secrets.values) != 0 {
		t.Fatal("partial Keychain write could not be revoked", err)
	}
}

func TestConnectionUsesCanonicalDurableConfigContract(t *testing.T) {
	dir := t.TempDir()
	registry, err := capability.Builtins()
	if err != nil {
		t.Fatal(err)
	}
	initial, err := config.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewManager(dir, registry, initial)
	if err != nil {
		t.Fatal(err)
	}
	instances, err := capability.NewInstances(registry, initial.Capabilities)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := capability.NewLifecycleEngine(instances, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	secrets := &memorySecrets{}
	connections, err := NewConnections(manager, &memoryAudit{}, lifecycle, func() (secretstore.Store, error) { return secrets, nil })
	if err != nil {
		t.Fatal(err)
	}
	connections.probe = func(_, _ string, _ secretstore.Secret) (serviceProbe, error) {
		return fixedProbe{status: Status{Version: SupportedVersion, Running: true}}, nil
	}
	if _, err := connections.Connect(context.Background(), "https://192.0.2.5", "reader", secretstore.NewSecret([]byte("private-password"))); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(dir, config.Filename))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "private-password") || !strings.Contains(string(contents), "credential_ref") || !strings.Contains(string(contents), `"ownership": "external"`) {
		t.Fatalf("invalid persisted connection: %s", contents)
	}
	if err := connections.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	contents, err = os.ReadFile(filepath.Join(dir, config.Filename))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "adguard-home") || len(secrets.values) != 0 {
		t.Fatal("connection not removed")
	}
}
