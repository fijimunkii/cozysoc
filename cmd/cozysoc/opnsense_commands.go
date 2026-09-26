package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/opnsense"
	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
)

type opnsenseCommandClient interface {
	ConnectOPNsense(context.Context, api.OPNsenseConnectParams) (api.OPNsenseConnection, error)
	DisconnectOPNsense(context.Context) (api.OPNsenseConnection, error)
}

func runOPNsenseConnectCommand(ctx context.Context, args []string, stdout, stderr *os.File) (err error) {
	fs := flag.NewFlagSet("opnsense-connect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	endpoint := fs.String("endpoint", "", "approved private HTTPS IP-literal origin")
	trustFile := fs.String("trust-pem-file", "", "optional explicitly approved PEM certificate")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *endpoint == "" {
		return errors.New("usage: cozysoc opnsense-connect [--state-dir PATH] --endpoint HTTPS_IP_ORIGIN [--trust-pem-file PATH]")
	}
	var trust []byte
	if *trustFile != "" {
		trust, err = readOPNsenseTrust(*trustFile)
		if err != nil {
			return err
		}
	}
	if _, err := opnsense.NewClient(*endpoint, "preflight", secretstore.NewSecret([]byte("preflight")), trust); err != nil {
		return errors.New("OPNsense endpoint or certificate is invalid")
	}
	terminal, err := openAdGuardTerminal(os.Stdin, stdout)
	if err != nil {
		return errors.New("OPNsense commands require a normal-user foreground macOS terminal")
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
	return performOPNsenseConnect(ctx, localapi.NewClient(dir), terminal, *endpoint, trust)
}

func readOPNsenseTrust(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 32<<10 {
		return nil, errors.New("OPNsense trust file must be a nonempty regular PEM file of at most 32 KiB")
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 || len(data) > 32<<10 {
		return nil, errors.New("unable to read OPNsense trust file")
	}
	return data, nil
}

func performOPNsenseConnect(ctx context.Context, client opnsenseCommandClient, terminal adguardTerminal, endpoint string, trust []byte) error {
	if _, err := opnsense.NewClient(endpoint, "preflight", secretstore.NewSecret([]byte("preflight")), trust); err != nil {
		return errors.New("OPNsense endpoint or certificate is invalid")
	}
	decisionCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	trustDetail := "System certificate roots only."
	if len(trust) > 0 {
		digest := sha256.Sum256(trust)
		trustDetail = "Additional certificate PEM SHA-256: " + hex.EncodeToString(digest[:]) + ". Confirm this fingerprint out of band."
	}
	disclosure := fmt.Sprintf("\nConnect Cozy SOC to OPNsense at %s?\n%s\nSetup reads version status only and saves the API key, secret and optional certificate in protected local storage. This candidate has not passed an owned-router privilege or recovery lab. It will not read neighbor tables during setup or change router settings.\n", endpoint, trustDetail)
	if err := terminal.Write(decisionCtx, disclosure); err != nil {
		return err
	}
	if err := terminal.FlushInput(); err != nil {
		return err
	}
	if err := terminal.Write(decisionCtx, "Type connect to approve. Anything else declines.\nApproval (default: decline): "); err != nil {
		return err
	}
	line, err := terminal.ReadLine(decisionCtx)
	if err != nil {
		return err
	}
	if err := gatewayPromptContext(decisionCtx); err != nil {
		return err
	}
	if line != "connect\n" {
		return errors.New("OPNsense connection declined")
	}
	if err := terminal.Write(decisionCtx, "OPNsense API key (input hidden): "); err != nil {
		return err
	}
	key, err := terminal.ReadSecretLine(decisionCtx)
	if err != nil {
		return err
	}
	defer clearOPNsenseBytes(key)
	if err := terminal.Write(decisionCtx, "\nOPNsense API secret (input hidden): "); err != nil {
		return err
	}
	secret, err := terminal.ReadSecretLine(decisionCtx)
	if err != nil {
		return err
	}
	defer clearOPNsenseBytes(secret)
	if err := terminal.Write(decisionCtx, "\n"); err != nil {
		return err
	}
	if err := gatewayPromptContext(decisionCtx); err != nil {
		return err
	}
	result, err := client.ConnectOPNsense(decisionCtx, api.OPNsenseConnectParams{Endpoint: endpoint, APIKey: string(key), APISecret: string(secret), TrustPEM: string(trust)})
	if err != nil {
		return opnsenseCommandError(err)
	}
	return writeOPNsenseResult(decisionCtx, terminal, result)
}

func clearOPNsenseBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
}

func runOPNsenseStatusCommand(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("opnsense-status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("opnsense-status takes flags only")
	}
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}
	result, err := localapi.NewClient(dir).OPNsenseStatus(ctx)
	if err != nil {
		return opnsenseCommandError(err)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func runOPNsenseDisconnectCommand(ctx context.Context, args []string, stdout, stderr *os.File) (err error) {
	fs := flag.NewFlagSet("opnsense-disconnect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("opnsense-disconnect takes flags only")
	}
	terminal, err := openAdGuardTerminal(os.Stdin, stdout)
	if err != nil {
		return errors.New("OPNsense commands require a normal-user foreground macOS terminal")
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
	return performOPNsenseDisconnect(ctx, localapi.NewClient(dir), terminal)
}

func performOPNsenseDisconnect(ctx context.Context, client opnsenseCommandClient, terminal adguardTerminal) error {
	decisionCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := terminal.Write(decisionCtx, "\nDisconnect Cozy SOC from OPNsense? This disables the local connection and removes its protected credential. It does not change the router.\n"); err != nil {
		return err
	}
	if err := terminal.FlushInput(); err != nil {
		return err
	}
	if err := terminal.Write(decisionCtx, "Type disconnect to approve. Anything else declines.\nApproval (default: decline): "); err != nil {
		return err
	}
	line, err := terminal.ReadLine(decisionCtx)
	if err != nil {
		return err
	}
	if err := gatewayPromptContext(decisionCtx); err != nil {
		return err
	}
	if line != "disconnect\n" {
		return errors.New("OPNsense disconnect declined")
	}
	result, err := client.DisconnectOPNsense(decisionCtx)
	if err != nil {
		return opnsenseCommandError(err)
	}
	return writeOPNsenseResult(decisionCtx, terminal, result)
}

func writeOPNsenseResult(ctx context.Context, terminal adguardTerminal, result api.OPNsenseConnection) error {
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return errors.New("unable to show OPNsense status")
	}
	return terminal.Write(ctx, string(encoded)+"\n")
}

func opnsenseCommandError(err error) error {
	var response *localapi.ResponseError
	if errors.As(err, &response) {
		return errors.New(response.Message)
	}
	return errors.New("OPNsense connection result unavailable; check local status before retrying")
}
