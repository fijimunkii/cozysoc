package main

import (
	"context"
	"fmt"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
)

type deviceWatchOperationalActivity interface {
	OperationalHealth(context.Context, time.Time) (devicewatch.OperationalHealth, error)
}

func (c *deviceWatchControl) OperationalHealth(ctx context.Context, at time.Time) (devicewatch.OperationalHealth, error) {
	if c == nil || c.activity == nil {
		return devicewatch.OperationalHealth{}, fmt.Errorf("Device Watch operational health is unavailable")
	}
	activity, ok := c.activity.(deviceWatchOperationalActivity)
	if !ok {
		return devicewatch.OperationalHealth{}, fmt.Errorf("Device Watch activity does not expose operational health")
	}
	return activity.OperationalHealth(ctx, at)
}
