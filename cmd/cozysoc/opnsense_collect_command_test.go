package main

import (
	"context"
	"strings"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/opnsense"
)

type opnsenseCollectPromptClient struct {
	calls  int
	params api.OPNsenseCollectParams
}

func (c *opnsenseCollectPromptClient) CollectOPNsense(_ context.Context, params api.OPNsenseCollectParams) (api.OPNsenseCollection, error) {
	c.calls++
	c.params = params
	return api.OPNsenseCollection{ScopeID: params.ScopeID, Read: 1, Inserted: 1}, nil
}

func TestOPNsenseCollectionRequiresExactForegroundReview(t *testing.T) {
	connection := api.OPNsenseConnection{Connected: true, Endpoint: "https://192.168.1.1", Version: opnsense.SupportedVersion}
	enrolled := api.EnrolledNetwork{ScopeID: "scope.home", Interface: api.NetworkInterface{InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.1.0/24"}}}
	for _, line := range []string{"", "yes\n", "collect\n", "collect scope.other\n", "collect scope.home", "collect scope.home\nextra"} {
		client := &opnsenseCollectPromptClient{}
		term := &opnsensePromptTerminal{line: line}
		if err := performOPNsenseCollect(context.Background(), client, term, "scope.home", connection, enrolled); err == nil || client.calls != 0 || term.flushes != 1 {
			t.Fatalf("unapproved router read %q: %v, calls %d", line, err, client.calls)
		}
	}
	client := &opnsenseCollectPromptClient{}
	term := &opnsensePromptTerminal{line: "collect scope.home\n"}
	if err := performOPNsenseCollect(context.Background(), client, term, "scope.home", connection, enrolled); err != nil || client.calls != 1 || client.params.Expected.Endpoint != connection.Endpoint || client.params.Expected.Interface.Prefixes[0] != "192.168.1.0/24" {
		t.Fatalf("review binding was lost: %+v %v", client.params, err)
	}
	if !strings.Contains(strings.Join(term.writes, ""), "not prove current device presence") {
		t.Fatal("coverage limitation was not disclosed")
	}
	connection.Endpoint = "https://example.com\nunsafe"
	term = &opnsensePromptTerminal{line: "collect scope.home\n"}
	if err := performOPNsenseCollect(context.Background(), client, term, "scope.home", connection, enrolled); err == nil || len(term.writes) != 0 || client.calls != 1 {
		t.Fatal("invalid origin displayed or read", err)
	}
}
