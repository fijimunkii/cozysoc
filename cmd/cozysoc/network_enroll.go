package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
)

func runNetworkEnrollCommand(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("network-enroll", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("network-enroll requires INTERFACE")
	}

	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}
	result, err := localapi.NewClient(dir).CallWithParams(ctx, api.MethodNetworkEnroll, api.NetworkEnrollParams{
		InterfaceName: fs.Arg(0),
	})
	if err != nil {
		return err
	}
	var pretty api.NetworkEnrollResult
	if err := json.Unmarshal(result, &pretty); err != nil {
		return fmt.Errorf("decode network enrollment response: %w", err)
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(pretty)
}
