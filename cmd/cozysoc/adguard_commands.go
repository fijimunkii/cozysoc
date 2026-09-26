package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/adguard"
	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
)

type adguardTerminal interface {
	gatewayCheckTerminal
	ReadSecretLine(context.Context) ([]byte, error)
}

type adguardCommandClient interface {
	ConnectAdGuard(context.Context, api.AdGuardConnectParams) (api.AdGuardConnection, error)
	DisconnectAdGuard(context.Context) (api.AdGuardConnection, error)
}

type adguardCollectionClient interface {
	CollectAdGuard(context.Context, string) (api.AdGuardCollection, error)
}

var errAdGuardTerminal = errors.New("AdGuard Home commands require a normal-user foreground macOS terminal; pipes and unattended approval are unavailable")

func runAdGuardCollectCommand(ctx context.Context, args []string, stdout, stderr *os.File) (err error) {
	fs := flag.NewFlagSet("adguard-collect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 || !deviceIDPattern.MatchString(fs.Arg(0)) {
		return errors.New("usage: cozysoc adguard-collect [--state-dir PATH] ENROLLED_SCOPE_ID")
	}
	terminal, err := openGatewayTerminal(os.Stdin, stdout)
	if err != nil {
		return errAdGuardTerminal
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
	return performAdGuardCollect(ctx, localapi.NewClient(dir), terminal, fs.Arg(0))
}

func performAdGuardCollect(ctx context.Context, client adguardCollectionClient, terminal gatewayCheckTerminal, scopeID string) error {
	if !deviceIDPattern.MatchString(scopeID) {
		return errors.New("invalid enrolled network scope")
	}
	decisionCtx, cancel := adguardCommandDeadline(ctx)
	defer cancel()
	disclosure := fmt.Sprintf("\nRead up to 100 recent AdGuard Home DNS queries for enrolled scope %s?\nOnly requests whose visible client IP is inside the scope's current enrolled prefixes will be stored as local evidence for 24 hours. DNS names are private browsing data. Missing/anonymized and out-of-scope clients are skipped. This does not identify a device, prove all clients use this resolver, or change DNS settings. The newest 100-query API limit may leave a history gap.\n", scopeID)
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
		return errors.New("AdGuard Home collection declined")
	}
	result, err := client.CollectAdGuard(decisionCtx, scopeID)
	if err != nil {
		return adguardCommandError(err)
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return errors.New("unable to show AdGuard Home collection result")
	}
	return terminal.Write(decisionCtx, string(encoded)+"\n")
}

func runAdGuardStatusCommand(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("adguard-status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("adguard-status takes flags only")
	}
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}
	result, err := localapi.NewClient(dir).AdGuardStatus(ctx)
	if err != nil {
		return adguardCommandError(err)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func runAdGuardConnectCommand(ctx context.Context, args []string, stdout, stderr *os.File) (err error) {
	fs := flag.NewFlagSet("adguard-connect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	endpoint := fs.String("endpoint", "", "approved AdGuard Home IP-literal origin")
	username := fs.String("username", "", "optional service username")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *endpoint == "" {
		return errors.New("usage: cozysoc adguard-connect [--state-dir PATH] --endpoint IP_ORIGIN [--username USER]")
	}
	terminal, err := openAdGuardTerminal(os.Stdin, stdout)
	if err != nil {
		return errAdGuardTerminal
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
	return performAdGuardConnect(ctx, localapi.NewClient(dir), terminal, *endpoint, *username)
}

func runAdGuardDisconnectCommand(ctx context.Context, args []string, stdout, stderr *os.File) (err error) {
	fs := flag.NewFlagSet("adguard-disconnect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("adguard-disconnect takes flags only")
	}
	terminal, err := openAdGuardTerminal(os.Stdin, stdout)
	if err != nil {
		return errAdGuardTerminal
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
	return performAdGuardDisconnect(ctx, localapi.NewClient(dir), terminal)
}

func adguardCommandDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 2*time.Minute)
}

func performAdGuardConnect(ctx context.Context, client adguardCommandClient, terminal adguardTerminal, endpoint, username string) error {
	placeholder := secretstore.Secret{}
	if username != "" {
		placeholder = secretstore.NewSecret([]byte{1})
	}
	if _, err := adguard.NewClient(endpoint, username, placeholder); err != nil {
		return errors.New("AdGuard Home endpoint or username is invalid")
	}
	decisionCtx, cancel := adguardCommandDeadline(ctx)
	defer cancel()
	if err := terminal.Write(decisionCtx, fmt.Sprintf("\nConnect read-only to AdGuard Home at %s?\nCozy SOC will read status, filtering state and query-log settings now; it will not read query history during setup or change DNS settings. The external owner keeps control of the service. The controller will store the approved origin and a Keychain reference if a credential is supplied.\n", endpoint)); err != nil {
		return err
	}
	if err := terminal.FlushInput(); err != nil {
		return err
	}
	if err := terminal.Write(decisionCtx, "Type connect to approve this connection. Anything else declines.\nApproval (default: decline): "); err != nil {
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
		return errors.New("AdGuard Home connection declined")
	}
	var password []byte
	if username != "" {
		if err := terminal.Write(decisionCtx, "AdGuard Home password (input hidden): "); err != nil {
			return err
		}
		password, err = terminal.ReadSecretLine(decisionCtx)
		if err != nil {
			return err
		}
		defer func() {
			for i := range password {
				password[i] = 0
			}
		}()
		if err := terminal.Write(decisionCtx, "\n"); err != nil {
			return err
		}
	}
	if err := gatewayPromptContext(decisionCtx); err != nil {
		return err
	}
	result, err := client.ConnectAdGuard(decisionCtx, api.AdGuardConnectParams{Endpoint: endpoint, Username: username, Password: string(password)})
	if err != nil {
		return adguardCommandError(err)
	}
	return writeAdGuardResult(decisionCtx, terminal, result)
}

func performAdGuardDisconnect(ctx context.Context, client adguardCommandClient, terminal adguardTerminal) error {
	decisionCtx, cancel := adguardCommandDeadline(ctx)
	defer cancel()
	if err := terminal.Write(decisionCtx, "\nDisconnect Cozy SOC from AdGuard Home? This removes the local connection and credential. It does not change or stop the external DNS service.\n"); err != nil {
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
		return errors.New("AdGuard Home disconnect declined")
	}
	result, err := client.DisconnectAdGuard(decisionCtx)
	if err != nil {
		return adguardCommandError(err)
	}
	return writeAdGuardResult(decisionCtx, terminal, result)
}

func writeAdGuardResult(ctx context.Context, terminal adguardTerminal, result api.AdGuardConnection) error {
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return errors.New("unable to show AdGuard Home status")
	}
	return terminal.Write(ctx, string(encoded)+"\n")
}

func adguardCommandError(err error) error {
	var response *localapi.ResponseError
	if errors.As(err, &response) {
		return errors.New(response.Message)
	}
	return errors.New("AdGuard Home connection result unavailable; check local status before retrying")
}
