package core

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/capability"
)

const gapMultiplier = 3

type Controller struct {
	version       string
	configVersion int
	startedAt     time.Time
	tickInterval  time.Duration
	capabilities  *capability.Instances

	mu        sync.RWMutex
	lastTick  time.Time
	gapCount  uint64
	lastGapAt *time.Time
}

func New(version string, configVersion int, tickInterval time.Duration, capabilities *capability.Instances) *Controller {
	now := time.Now()
	return &Controller{
		version:       version,
		configVersion: configVersion,
		startedAt:     now,
		tickInterval:  tickInterval,
		lastTick:      now,
		capabilities:  capabilities,
	}
}

func (c *Controller) Version() string {
	return c.version
}

func (c *Controller) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(c.tickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.RecordTick(time.Now())
			}
		}
	}()
}

func (c *Controller) RecordTick(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if now.Sub(c.lastTick) > c.tickInterval*gapMultiplier {
		gapAt := now.UTC()
		c.gapCount++
		c.lastGapAt = &gapAt
	}
	c.lastTick = now
}

func (c *Controller) Status() api.Status {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return api.Status{
		APIVersion:          api.Version,
		ControllerVersion:   c.version,
		PID:                 os.Getpid(),
		StartedAt:           c.startedAt.UTC(),
		UptimeMS:            time.Since(c.startedAt).Milliseconds(),
		ConfigSchemaVersion: c.configVersion,
		Transport:           "unix",
	}
}

func (c *Controller) Health() api.Health {
	c.mu.RLock()
	defer c.mu.RUnlock()

	state := "ok"
	if time.Since(c.lastTick) > c.tickInterval*gapMultiplier {
		state = "degraded"
	}

	var lastGap *time.Time
	if c.lastGapAt != nil {
		copyValue := *c.lastGapAt
		lastGap = &copyValue
	}
	return api.Health{
		State:      state,
		LastTickAt: c.lastTick.UTC(),
		GapCount:   c.gapCount,
		LastGapAt:  lastGap,
	}
}

func (c *Controller) Capabilities() api.CapabilityList {
	var instances []capability.Instance
	if c.capabilities != nil {
		instances = c.capabilities.List()
	}
	return api.CapabilityList{
		CatalogSchemaVersion: capability.SchemaVersion,
		Capabilities:         instances,
	}
}
