package storage

import (
	"context"
	"fmt"
)

type HealthState string

const (
	HealthCurrent   HealthState = "current"
	HealthPressure  HealthState = "pressure"
	HealthAtQuota   HealthState = "at-quota"
)

type Health struct {
	State         HealthState
	DatabaseBytes int64
	MaxBytes      int64
}

func (s *Store) Health(ctx context.Context) (Health, error) {
	if s == nil || s.conn == nil {
		return Health{}, fmt.Errorf("storage is unavailable")
	}
	bytes, err := s.DatabaseBytes(ctx)
	if err != nil {
		return Health{}, err
	}
	health := Health{
		State:         HealthCurrent,
		DatabaseBytes: bytes,
		MaxBytes:      s.limits.MaxBytes,
	}
	if health.MaxBytes <= 0 {
		return Health{}, fmt.Errorf("storage quota is unavailable")
	}
	switch {
	case health.DatabaseBytes >= health.MaxBytes:
		health.State = HealthAtQuota
	case health.DatabaseBytes*10 >= health.MaxBytes*9:
		health.State = HealthPressure
	}
	return health, nil
}
