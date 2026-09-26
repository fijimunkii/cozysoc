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

func runDeviceSplitCommand(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("device-split", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 || fs.NArg() > 3 {
		return fmt.Errorf("device-split requires SOURCE_DEVICE_ID OBSERVATION_ID [TARGET_DEVICE_ID]")
	}
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}
	params := api.DeviceSplitParams{SourceDeviceID: fs.Arg(0), ObservationID: fs.Arg(1)}
	if fs.NArg() == 3 {
		params.TargetDeviceID = fs.Arg(2)
	}
	return runDeviceSplitCorrection(ctx, dir, api.MethodDeviceSplit, params, stdout)
}

func runDeviceUnsplitCommand(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("device-unsplit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("device-unsplit requires OBSERVATION_ID")
	}
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}
	return runDeviceSplitCorrection(ctx, dir, api.MethodDeviceUnsplit, api.DeviceUnsplitParams{ObservationID: fs.Arg(0)}, stdout)
}

func runDeviceSplitsCommand(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("device-splits", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("device-splits accepts no positional arguments")
	}
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}
	result, err := localapi.NewClient(dir).Call(ctx, api.MethodDeviceSplits)
	if err != nil {
		return err
	}
	var splits api.DeviceSplitList
	if err := json.Unmarshal(result, &splits); err != nil {
		return fmt.Errorf("decode device split list: %w", err)
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(splits)
}

func runDeviceSplitCorrection(ctx context.Context, dir, method string, params any, stdout *os.File) error {
	result, err := localapi.NewClient(dir).CallWithParams(ctx, method, params)
	if err != nil {
		return err
	}
	var correction api.DeviceSplitResult
	if err := json.Unmarshal(result, &correction); err != nil {
		return fmt.Errorf("decode device split result: %w", err)
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(correction)
}
