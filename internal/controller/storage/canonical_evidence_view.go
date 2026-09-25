package storage

import (
	"context"
	"database/sql"
	"fmt"
)

// CanonicalEvidenceView serves Device Watch evidence from both retained storage
// formats. Each call takes one read-only snapshot on the separate history pool,
// so a batch rewrite cannot split one response across different database states.
// Other Store operations remain available through the embedded pointer.
type CanonicalEvidenceView struct{ *Store }

func NewCanonicalEvidenceView(store *Store) (*CanonicalEvidenceView, error) {
	if store == nil || store.gatewayHistoryDB == nil || store.now == nil {
		return nil, fmt.Errorf("canonical evidence view requires an open store")
	}
	return &CanonicalEvidenceView{Store: store}, nil
}

func withCanonicalEvidenceSnapshot[T any](ctx context.Context, view *CanonicalEvidenceView, read func(*MixedIdentitySnapshot) (T, error)) (T, error) {
	var empty T
	if view == nil || view.Store == nil || view.gatewayHistoryDB == nil {
		return empty, fmt.Errorf("canonical evidence view is unavailable")
	}
	tx, err := view.gatewayHistoryDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	snapshot, err := NewMixedIdentitySnapshot(tx, view.now().UTC())
	if err != nil {
		return empty, err
	}
	return read(snapshot)
}

func (v *CanonicalEvidenceView) ListDeviceEvidence(ctx context.Context, query DeviceEvidenceQuery) (DeviceEvidencePage, error) {
	return withCanonicalEvidenceSnapshot(ctx, v, func(snapshot *MixedIdentitySnapshot) (DeviceEvidencePage, error) {
		return snapshot.ListDeviceEvidence(ctx, query)
	})
}

func (v *CanonicalEvidenceView) ListDevicesForScope(ctx context.Context, query DeviceQuery) (DevicePage, error) {
	return withCanonicalEvidenceSnapshot(ctx, v, func(snapshot *MixedIdentitySnapshot) (DevicePage, error) {
		return snapshot.ListDevicesForScope(ctx, query)
	})
}

func (v *CanonicalEvidenceView) GetDeviceEvidenceDetail(ctx context.Context, query DeviceEvidenceDetailQuery) (DeviceEvidenceDetail, error) {
	return withCanonicalEvidenceSnapshot(ctx, v, func(snapshot *MixedIdentitySnapshot) (DeviceEvidenceDetail, error) {
		return snapshot.GetDeviceEvidenceDetail(ctx, query)
	})
}

func (v *CanonicalEvidenceView) ListDeviceActivity(ctx context.Context, query DeviceActivityQuery) (DeviceActivityPage, error) {
	return withCanonicalEvidenceSnapshot(ctx, v, func(snapshot *MixedIdentitySnapshot) (DeviceActivityPage, error) {
		return snapshot.ListDeviceActivity(ctx, query)
	})
}

func (v *CanonicalEvidenceView) ListObservations(ctx context.Context, query ObservationQuery) (ObservationPage, error) {
	return withCanonicalEvidenceSnapshot(ctx, v, func(snapshot *MixedIdentitySnapshot) (ObservationPage, error) {
		return snapshot.ListObservations(ctx, query)
	})
}
