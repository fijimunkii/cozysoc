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

func runDeviceLabelCommand(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("device-label", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("device-label requires DEVICE_ID and LABEL")
	}

	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}
	label := fs.Arg(1)
	params := api.DeviceLabelParams{DeviceID: fs.Arg(0), Label: &label}
	result, err := localapi.NewClient(dir).CallWithParams(ctx, api.MethodDeviceLabel, params)
	if err != nil {
		return err
	}
	var pretty api.DeviceLabelResult
	if err := json.Unmarshal(result, &pretty); err != nil {
		return fmt.Errorf("decode device label response: %w", err)
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(pretty)
}
