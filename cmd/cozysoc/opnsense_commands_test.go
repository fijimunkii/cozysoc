package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

type opnsensePromptTerminal struct {
	writes  []string
	line    string
	secrets [][]byte
	reads   int
	flushes int
}

func (t *opnsensePromptTerminal) Write(_ context.Context, value string) error {
	t.writes = append(t.writes, value)
	return nil
}
func (t *opnsensePromptTerminal) FlushInput() error                        { t.flushes++; return nil }
func (t *opnsensePromptTerminal) ReadLine(context.Context) (string, error) { return t.line, nil }
func (t *opnsensePromptTerminal) ReadSecretLine(context.Context) ([]byte, error) {
	value := t.secrets[t.reads]
	t.reads++
	return value, nil
}
func (t *opnsensePromptTerminal) Close() error { return nil }

type opnsensePromptClient struct {
	connects, disconnects int
	params                api.OPNsenseConnectParams
}

func (c *opnsensePromptClient) ConnectOPNsense(_ context.Context, p api.OPNsenseConnectParams) (api.OPNsenseConnection, error) {
	c.connects++
	c.params = p
	return api.OPNsenseConnection{Connected: true}, nil
}
func (c *opnsensePromptClient) DisconnectOPNsense(context.Context) (api.OPNsenseConnection, error) {
	c.disconnects++
	return api.OPNsenseConnection{}, nil
}

func TestOPNsenseCommandRequiresConsentBeforeReadingSecrets(t *testing.T) {
	for _, line := range []string{"", "yes\n", "connect", "Connect\n", "connect\nextra"} {
		term := &opnsensePromptTerminal{line: line}
		client := &opnsensePromptClient{}
		if err := performOPNsenseConnect(context.Background(), client, term, "https://192.168.1.1", nil); err == nil || client.connects != 0 || term.reads != 0 || term.flushes != 1 {
			t.Fatalf("unapproved connection %q: %v", line, err)
		}
	}
	key, secret := []byte("private-key"), []byte("private-secret")
	term := &opnsensePromptTerminal{line: "connect\n", secrets: [][]byte{key, secret}}
	client := &opnsensePromptClient{}
	if err := performOPNsenseConnect(context.Background(), client, term, "https://192.168.1.1", nil); err != nil {
		t.Fatal(err)
	}
	if client.connects != 1 || client.params.APIKey != "private-key" || client.params.APISecret != "private-secret" || term.reads != 2 || strings.Contains(strings.Join(term.writes, ""), "private-key") || strings.Contains(strings.Join(term.writes, ""), "private-secret") {
		t.Fatal("secret prompt or result was unsafe")
	}
	for _, data := range [][]byte{key, secret} {
		for _, value := range data {
			if value != 0 {
				t.Fatal("secret buffer was not erased")
			}
		}
	}
}

func TestOPNsenseCommandRejectsUnsafeEndpointAndDisconnectNeedsConsent(t *testing.T) {
	term := &opnsensePromptTerminal{line: "connect\n"}
	client := &opnsensePromptClient{}
	if err := performOPNsenseConnect(context.Background(), client, term, "https://example.com\nunsafe", nil); err == nil || client.connects != 0 || len(term.writes) != 0 {
		t.Fatal("invalid endpoint shown or connected", err)
	}
	if err := performOPNsenseDisconnect(context.Background(), client, &opnsensePromptTerminal{line: "yes\n"}); err == nil || client.disconnects != 0 {
		t.Fatal("unapproved disconnect", err)
	}
	if err := performOPNsenseDisconnect(context.Background(), client, &opnsensePromptTerminal{line: "disconnect\n"}); err != nil || client.disconnects != 1 {
		t.Fatal("approved disconnect failed", err)
	}
	if got := opnsenseCommandError(errors.New("private details")); strings.Contains(got.Error(), "private") {
		t.Fatal(got)
	}
}

func TestOPNsenseTrustRequiresRegularBoundedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.pem")
	if err := os.WriteFile(path, []byte("certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := readOPNsenseTrust(path); err != nil || string(data) != "certificate" {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.pem")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readOPNsenseTrust(link); err == nil {
		t.Fatal("symlink trust file accepted")
	}
}
