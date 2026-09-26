package opnsense

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

type connectionAudit struct {
	phases []string
	fail   string
}

func (a *connectionAudit) InsertAuditEvent(_ context.Context, event domain.AuditEvent) error {
	if err := domain.ValidateAuditEvent(event); err != nil {
		return err
	}
	var payload struct {
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	if payload.Phase == a.fail {
		return errors.New("injected audit failure")
	}
	a.phases = append(a.phases, payload.Phase)
	return nil
}

type connectionLifecycle struct{ desired capability.DesiredState }

func (l *connectionLifecycle) ApplyConfiguration(value capability.Configuration) error {
	l.desired = value.Desired
	return nil
}
func (l *connectionLifecycle) RemoveConfiguration(string) error {
	l.desired = capability.DesiredDisabled
	return nil
}

type connectionSecrets struct {
	values       map[string]secretstore.Secret
	deleteErr    error
	beforeDelete func()
}

func (s *connectionSecrets) Put(_ context.Context, ref secretstore.Reference, value secretstore.Secret) error {
	if s.values == nil {
		s.values = map[string]secretstore.Secret{}
	}
	s.values[ref.String()] = secretstore.NewSecret(value.Bytes())
	return nil
}
func (s *connectionSecrets) Get(_ context.Context, ref secretstore.Reference) (secretstore.Secret, error) {
	if value, ok := s.values[ref.String()]; ok {
		return value, nil
	}
	return secretstore.Secret{}, secretstore.ErrNotFound
}
func (s *connectionSecrets) Delete(_ context.Context, ref secretstore.Reference) error {
	if s.beforeDelete != nil {
		s.beforeDelete()
	}
	if s.deleteErr != nil {
		return s.deleteErr
	}
	if _, ok := s.values[ref.String()]; !ok {
		return secretstore.ErrNotFound
	}
	delete(s.values, ref.String())
	return nil
}
func (s *connectionSecrets) Backend() secretstore.BackendInfo {
	return secretstore.BackendInfo{Kind: "test", Persistent: true}
}

type connectionProbe struct{ status Status }

func (p connectionProbe) Probe(context.Context) (Status, error) { return p.status, nil }
func (p connectionProbe) ReadNeighbors(context.Context) (Snapshot, error) {
	return Snapshot{Status: p.status}, nil
}

func newConnectionFixture(t *testing.T) (*Connections, *config.Manager, *connectionAudit, *connectionLifecycle, *connectionSecrets, string) {
	t.Helper()
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
	audit := &connectionAudit{}
	lifecycle := &connectionLifecycle{}
	secrets := &connectionSecrets{}
	connections, err := NewConnections(manager, audit, lifecycle, func() (secretstore.Store, error) { return secrets, nil })
	if err != nil {
		t.Fatal(err)
	}
	connections.probe = func(endpoint, key string, secret secretstore.Secret, trust []byte) (serviceProbe, error) {
		if endpoint != "https://192.168.1.1" || key != "private-api-key" || string(secret.Bytes()) != "private-api-secret" || string(trust) != "private-certificate" {
			return nil, ErrResponse
		}
		return connectionProbe{status: Status{Version: SupportedVersion}}, nil
	}
	connections.now = func() time.Time { return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC) }
	return connections, manager, audit, lifecycle, secrets, dir
}

func TestConnectionUsesProtectedBundleAndDurableExternalIntent(t *testing.T) {
	c, manager, audit, lifecycle, secrets, dir := newConnectionFixture(t)
	connection, err := c.Connect(context.Background(), "https://192.168.1.1", "private-api-key", secretstore.NewSecret([]byte("private-api-secret")), []byte("private-certificate"))
	if err != nil || connection.Status.Version != SupportedVersion || lifecycle.desired != capability.DesiredEnabled {
		t.Fatalf("connection failed: %+v %v", connection, err)
	}
	configured, ok := manager.Capability(CapabilityID)
	if !ok || configured.Ownership != capability.OwnershipExternal || configured.Desired != capability.DesiredEnabled || len(secrets.values) != 1 {
		t.Fatalf("wrong durable connection: %+v, %v", configured, secrets.values)
	}
	data, err := os.ReadFile(filepath.Join(dir, config.Filename))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private-api-key", "private-api-secret", "private-certificate"} {
		if strings.Contains(string(data), private) {
			t.Fatalf("private credential leaked to config: %s", private)
		}
	}
	if strings.Join(audit.phases, ",") != "requested,applied" {
		t.Fatalf("unexpected audits: %v", audit.phases)
	}
	if current, err := c.Current(context.Background()); err != nil || current.Endpoint != connection.Endpoint {
		t.Fatalf("current status failed: %+v %v", current, err)
	}
	secrets.beforeDelete = func() {
		current, _ := manager.Capability(CapabilityID)
		if current.Desired != capability.DesiredDisabled || lifecycle.desired != capability.DesiredDisabled {
			t.Error("credential deletion ran before durable disable")
		}
	}
	if err := c.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.Capability(CapabilityID); ok || len(secrets.values) != 0 || strings.Join(audit.phases, ",") != "requested,applied,disconnect-requested,disconnect-applied" {
		t.Fatalf("disconnect left state: %v %v", secrets.values, audit.phases)
	}
}

func TestConnectionEndpointTamperCannotRedirectCredential(t *testing.T) {
	c, manager, _, _, _, _ := newConnectionFixture(t)
	if _, err := c.Connect(context.Background(), "https://192.168.1.1", "private-api-key", secretstore.NewSecret([]byte("private-api-secret")), []byte("private-certificate")); err != nil {
		t.Fatal(err)
	}
	configured, _ := manager.Capability(CapabilityID)
	configured.Values["endpoint"] = json.RawMessage(`"https://192.168.1.2"`)
	if _, _, err := manager.ReplaceCapability(CapabilityID, &configured); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Current(context.Background()); !errors.Is(err, ErrResponse) {
		t.Fatalf("redirected protected credential after config tamper: %v", err)
	}
}

func TestDisconnectFailureLeavesDisabledRetryableReference(t *testing.T) {
	c, manager, _, lifecycle, secrets, _ := newConnectionFixture(t)
	if _, err := c.Connect(context.Background(), "https://192.168.1.1", "private-api-key", secretstore.NewSecret([]byte("private-api-secret")), []byte("private-certificate")); err != nil {
		t.Fatal(err)
	}
	secrets.deleteErr = secretstore.ErrLocked
	if err := c.Disconnect(context.Background()); !errors.Is(err, secretstore.ErrLocked) {
		t.Fatalf("expected protected-store failure, got %v", err)
	}
	configured, ok := manager.Capability(CapabilityID)
	if !ok || configured.Desired != capability.DesiredDisabled || lifecycle.desired != capability.DesiredDisabled {
		t.Fatalf("unsafe state after failed deletion: %+v", configured)
	}
	if _, err := c.Current(context.Background()); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("disabled intent read as connected: %v", err)
	}
	secrets.deleteErr = nil
	if err := c.Disconnect(context.Background()); err != nil || len(secrets.values) != 0 {
		t.Fatalf("retry did not revoke credential: %v %v", err, secrets.values)
	}
}

func TestConnectAuditFailureLeavesNoCredentialOrIntent(t *testing.T) {
	c, manager, audit, _, secrets, _ := newConnectionFixture(t)
	probes := 0
	c.probe = func(string, string, secretstore.Secret, []byte) (serviceProbe, error) {
		probes++
		return connectionProbe{status: Status{Version: SupportedVersion}}, nil
	}
	audit.fail = "requested"
	if _, err := c.Connect(context.Background(), "https://192.168.1.1", "private-api-key", secretstore.NewSecret([]byte("private-api-secret")), []byte("private-certificate")); err == nil {
		t.Fatal("audit failure accepted")
	}
	if _, ok := manager.Capability(CapabilityID); ok || len(secrets.values) != 0 || probes != 0 {
		t.Fatalf("audit failure left connection: %v %v", ok, secrets.values)
	}
}
