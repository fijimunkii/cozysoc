package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
)

const defaultCoverageVerificationInterval = 15 * time.Second
const coverageVerificationTimeout = 5 * time.Second

type deviceWatchVerificationEngine interface {
	List() []capability.Instance
	Run(context.Context, string, capability.LifecycleAction) (capability.LifecycleResult, error)
}

func startDeviceWatchVerification(ctx context.Context, engine deviceWatchVerificationEngine, logger *slog.Logger) error {
	if engine == nil {
		return fmt.Errorf("Device Watch verification engine is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	go func() {
		verify := func() {
			if ctx.Err() != nil {
				return
			}
			verifyCtx, cancel := context.WithTimeout(ctx, coverageVerificationTimeout)
			defer cancel()
			if err := verifyDeviceWatch(verifyCtx, engine); err != nil {
				logger.Warn("device_watch_verification_failed")
			}
		}

		verify()
		ticker := time.NewTicker(defaultCoverageVerificationInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				verify()
			}
		}
	}()
	return nil
}

func verifyDeviceWatch(ctx context.Context, engine deviceWatchVerificationEngine) error {
	if engine == nil {
		return fmt.Errorf("Device Watch verification engine is required")
	}
	enabled := false
	for _, instance := range engine.List() {
		if instance.Manifest.ID == devicewatch.CapabilityID && instance.State.Desired == capability.DesiredEnabled {
			enabled = true
			break
		}
	}
	if !enabled {
		return nil
	}
	_, err := engine.Run(ctx, devicewatch.CapabilityID, capability.ActionVerify)
	if errors.Is(err, capability.ErrDesiredStateMismatch) {
		// A concurrent disable can win after the snapshot above. That is a normal
		// reconciliation race, not a verification failure.
		return nil
	}
	return err
}
