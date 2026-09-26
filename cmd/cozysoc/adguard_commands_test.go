package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

type adguardPromptTerminal struct {
	writes      []string
	line        string
	password    []byte
	flushes     int
	secretReads int
}

func (t *adguardPromptTerminal) Write(_ context.Context, s string) error {
	t.writes = append(t.writes, s)
	return nil
}
func (t *adguardPromptTerminal) FlushInput() error                        { t.flushes++; return nil }
func (t *adguardPromptTerminal) ReadLine(context.Context) (string, error) { return t.line, nil }
func (t *adguardPromptTerminal) ReadSecretLine(context.Context) ([]byte, error) {
	t.secretReads++
	return t.password, nil
}
func (t *adguardPromptTerminal) Close() error { return nil }

type adguardPromptClient struct {
	connects, disconnects int
	params                api.AdGuardConnectParams
}

type adguardCollectionPromptClient struct{ calls int }

func (c *adguardCollectionPromptClient) CollectAdGuard(_ context.Context, scopeID string) (api.AdGuardCollection, error) {
	c.calls++
	return api.AdGuardCollection{ScopeID: scopeID, Read: 2, Inserted: 1, SkippedOutsideScope: 1}, nil
}

func (c *adguardPromptClient) ConnectAdGuard(_ context.Context, p api.AdGuardConnectParams) (api.AdGuardConnection, error) {
	c.connects++
	c.params = p
	return api.AdGuardConnection{Connected: true, Endpoint: p.Endpoint, Running: true}, nil
}
func (c *adguardPromptClient) DisconnectAdGuard(context.Context) (api.AdGuardConnection, error) {
	c.disconnects++
	return api.AdGuardConnection{Connected: false}, nil
}

func TestAdGuardConnectionRequiresFreshTerminalConsentAndHidesPassword(t *testing.T) {
	for _, line := range []string{"", "yes\n", "connect", "Connect\n", "connect\nextra"} {
		term := &adguardPromptTerminal{line: line, password: []byte("private-password")}
		client := &adguardPromptClient{}
		if err := performAdGuardConnect(context.Background(), client, term, "https://192.0.2.5", "reader"); err == nil || client.connects != 0 || term.secretReads != 0 || term.flushes != 1 {
			t.Fatalf("approval %q accepted: err %v, calls %d", line, err, client.connects)
		}
	}
	term := &adguardPromptTerminal{line: "connect\n", password: []byte("private-password")}
	client := &adguardPromptClient{}
	if err := performAdGuardConnect(context.Background(), client, term, "https://192.0.2.5", "reader"); err != nil {
		t.Fatal(err)
	}
	if client.connects != 1 || client.params.Password != "private-password" || term.secretReads != 1 {
		t.Fatal("approved connection did not read one hidden credential")
	}
	if strings.Contains(strings.Join(term.writes, ""), "private-password") {
		t.Fatal("password echoed")
	}
	for _, b := range term.password {
		if b != 0 {
			t.Fatal("password buffer was not erased")
		}
	}
}

func TestAdGuardDisconnectRequiresConsent(t *testing.T) {
	client := &adguardPromptClient{}
	if err := performAdGuardDisconnect(context.Background(), client, &adguardPromptTerminal{line: "yes\n"}); err == nil || client.disconnects != 0 {
		t.Fatal("unapproved disconnect", err)
	}
	if err := performAdGuardDisconnect(context.Background(), client, &adguardPromptTerminal{line: "disconnect\n"}); err != nil || client.disconnects != 1 {
		t.Fatal("approved disconnect failed", err)
	}
}

func TestAdGuardPromptRejectsInvalidEndpointBeforeDisclosure(t *testing.T) {
	term := &adguardPromptTerminal{line: "connect\n"}
	client := &adguardPromptClient{}
	if err := performAdGuardConnect(context.Background(), client, term, "http://example.test\nunsafe", "reader"); err == nil || client.connects != 0 || len(term.writes) != 0 {
		t.Fatal("invalid endpoint shown or connected", err)
	}
	if got := adguardCommandError(errors.New("private details")); strings.Contains(got.Error(), "private") {
		t.Fatal(got)
	}
}

func TestAdGuardCollectionRequiresExactScopeConsent(t *testing.T) {
	for _, line := range []string{"collect\n", "yes\n", "collect scope.other\n", "collect scope.home", "collect scope.home\nextra"} {
		client := &adguardCollectionPromptClient{}
		terminal := &adguardPromptTerminal{line: line}
		if err := performAdGuardCollect(context.Background(), client, terminal, "scope.home"); err == nil || client.calls != 0 || terminal.flushes != 1 {
			t.Fatalf("unapproved collection %q: calls %d err %v", line, client.calls, err)
		}
	}
	client := &adguardCollectionPromptClient{}
	terminal := &adguardPromptTerminal{line: "collect scope.home\n"}
	if err := performAdGuardCollect(context.Background(), client, terminal, "scope.home"); err != nil || client.calls != 1 {
		t.Fatalf("approved collection failed: calls %d err %v", client.calls, err)
	}
	written := strings.Join(terminal.writes, "")
	if !strings.Contains(written, "private browsing data") || !strings.Contains(written, `"inserted": 1`) || strings.Contains(written, "private.example") {
		t.Fatalf("unsafe collection disclosure/result: %q", written)
	}
}
