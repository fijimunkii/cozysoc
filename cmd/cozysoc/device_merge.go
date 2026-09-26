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

func runDeviceMergeCommand(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("device-merge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("device-merge requires SOURCE_DEVICE_ID and TARGET_DEVICE_ID")
	}
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}
	return runIdentityCorrection(ctx, dir, api.MethodDeviceMerge,
		api.DeviceMergeParams{SourceDeviceID: fs.Arg(0), TargetDeviceID: fs.Arg(1)}, stdout)
}

func runDeviceUnmergeCommand(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("device-unmerge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("device-unmerge requires SOURCE_DEVICE_ID")
	}
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}
	return runIdentityCorrection(ctx, dir, api.MethodDeviceUnmerge,
		api.DeviceUnmergeParams{SourceDeviceID: fs.Arg(0)}, stdout)
}

func runDeviceMergesCommand(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("device-merges", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("device-merges accepts no positional arguments")
	}
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}
	result, err := localapi.NewClient(dir).Call(ctx, api.MethodDeviceMerges)
	if err != nil {
		return err
	}
	var merges api.DeviceMergeList
	if err := json.Unmarshal(result, &merges); err != nil {
		return fmt.Errorf("decode device merge list: %w", err)
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(merges)
}

func runIdentityCorrection(ctx context.Context, stateDir, method string, params any, stdout *os.File) error {
	result, err := localapi.NewClient(stateDir).CallWithParams(ctx, method, params)
	if err != nil {
		return err
	}
	var correction api.DeviceIdentityCorrectionResult
	if err := json.Unmarshal(result, &correction); err != nil {
		return fmt.Errorf("decode device identity response: %w", err)
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(correction)
}
