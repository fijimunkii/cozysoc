package storage

import (
	"context"
	"fmt"
)

type HealthState string

const (
	HealthCurrent  HealthState = "current"
	HealthPressure HealthState = "pressure"
	HealthAtQuota  HealthState = "at-quota"
)

type Health struct {
	State         HealthState
	DatabaseBytes int64
	UsedBytes     int64
	ReusableBytes int64
	MaxBytes      int64
}

func (s *Store) Health(ctx context.Context) (Health, error) {
	if s == nil || s.conn == nil {
		return Health{}, fmt.Errorf("storage is unavailable")
	}
	var pageCount, freePages, pageSize, maxPages int64
	for pragma, destination := range map[string]*int64{
		"PRAGMA page_count":     &pageCount,
		"PRAGMA freelist_count": &freePages,
		"PRAGMA page_size":      &pageSize,
		"PRAGMA max_page_count": &maxPages,
	} {
		if err := s.conn.QueryRowContext(ctx, pragma).Scan(destination); err != nil {
			return Health{}, fmt.Errorf("read storage health %s: %w", pragma, err)
		}
	}
	if pageSize <= 0 || maxPages <= 0 || freePages < 0 || freePages > pageCount {
		return Health{}, fmt.Errorf("storage quota metadata is invalid")
	}
	allocated := pageCount * pageSize
	reusable := freePages * pageSize
	used := (pageCount - freePages) * pageSize
	maxBytes := maxPages * pageSize
	health := Health{
		State:         classifyStorageHealth(used, maxBytes),
		DatabaseBytes: allocated,
		UsedBytes:     used,
		ReusableBytes: reusable,
		MaxBytes:      maxBytes,
	}
	return health, nil
}

func classifyStorageHealth(usedBytes, maxBytes int64) HealthState {
	if maxBytes <= 0 {
		return HealthAtQuota
	}
	if usedBytes >= maxBytes {
		return HealthAtQuota
	}
	// Ninety percent is an operational warning threshold against a known
	// database quota, not a security/protection score.
	if usedBytes >= maxBytes-maxBytes/10 {
		return HealthPressure
	}
	return HealthCurrent
}
