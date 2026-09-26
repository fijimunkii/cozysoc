package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/opnsense"
	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
)

type opnsenseCollectionClient interface {
	CollectOPNsense(context.Context, api.OPNsenseCollectParams) (api.OPNsenseCollection, error)
}

func runOPNsenseCollectCommand(ctx context.Context, args []string, stdout, stderr *os.File) (err error) {
	fs := flag.NewFlagSet("opnsense-collect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 || !deviceIDPattern.MatchString(fs.Arg(0)) {
		return errors.New("usage: cozysoc opnsense-collect [--state-dir PATH] ENROLLED_SCOPE_ID")
	}
	terminal, err := openGatewayTerminal(os.Stdin, stdout)
	if err != nil {
		return errors.New("OPNsense collection requires a normal-user foreground macOS terminal")
	}
	defer func() {
		if closeErr := terminal.Close(); closeErr != nil && err == nil {
			err = errors.New("terminal cleanup could not be confirmed")
		}
	}()
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}
	client := localapi.NewClient(dir)
	decisionCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	connection, err := client.OPNsenseStatus(decisionCtx)
	if err != nil {
		return opnsenseCommandError(err)
	}
	if !connection.Connected {
		return errors.New("OPNsense is not connected")
	}
	raw, err := client.Call(decisionCtx, api.MethodNetworksList)
	if err != nil {
		return opnsenseCommandError(err)
	}
	var networks api.NetworkList
	if err := json.Unmarshal(raw, &networks); err != nil || networks.Enrolled == nil || networks.Enrolled.ScopeID != fs.Arg(0) {
		return errors.New("selected enrolled network scope is unavailable")
	}
	return performOPNsenseCollect(decisionCtx, client, terminal, fs.Arg(0), connection, *networks.Enrolled)
}

func performOPNsenseCollect(ctx context.Context, client opnsenseCollectionClient, terminal gatewayCheckTerminal, scopeID string, connection api.OPNsenseConnection, enrolled api.EnrolledNetwork) error {
	if !deviceIDPattern.MatchString(scopeID) || !connection.Connected || connection.Version != opnsense.SupportedVersion || enrolled.ScopeID != scopeID ||
		devicewatch.ValidateScopeBinding(devicewatch.ScopeBinding{InterfaceName: enrolled.Interface.InterfaceName, InterfaceIndex: enrolled.Interface.InterfaceIndex, Prefixes: enrolled.Interface.Prefixes}) != nil {
		return errors.New("OPNsense connection or enrolled scope is unavailable")
	}
	if _, err := opnsense.NewClient(connection.Endpoint, "preflight", secretstore.NewSecret([]byte("preflight")), nil); err != nil {
		return errors.New("OPNsense connection endpoint is invalid")
	}
	decisionCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	disclosure := fmt.Sprintf("\nRead OPNsense ARP/NDP neighbor tables once from %s for enrolled scope %s (%s)?\nUp to 256 rows per address family will be considered; only addresses inside the selected prefixes become local 24-hour router-reported evidence. This may expose household IP and hardware addresses. Neighbor tables do not prove current device presence, identity, all clients, local traffic, or whole-network coverage. The router will not be changed.\n", connection.Endpoint, scopeID, strings.Join(enrolled.Interface.Prefixes, ", "))
	if err := terminal.Write(decisionCtx, disclosure); err != nil {
		return err
	}
	if err := terminal.FlushInput(); err != nil {
		return err
	}
	if err := terminal.Write(decisionCtx, "Type collect "+scopeID+" to approve this one read. Anything else declines.\nApproval (default: decline): "); err != nil {
		return err
	}
	line, err := terminal.ReadLine(decisionCtx)
	if err != nil {
		return err
	}
	if err := gatewayPromptContext(decisionCtx); err != nil {
		return err
	}
	if line != "collect "+scopeID+"\n" {
		return errors.New("OPNsense collection declined")
	}
	params := api.OPNsenseCollectParams{ScopeID: scopeID, Expected: api.OPNsenseCollectExpected{Endpoint: connection.Endpoint, Interface: enrolled.Interface}}
	result, err := client.CollectOPNsense(decisionCtx, params)
	if err != nil {
		return opnsenseCommandError(err)
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return errors.New("unable to show OPNsense collection result")
	}
	return terminal.Write(decisionCtx, string(encoded)+"\n")
}
