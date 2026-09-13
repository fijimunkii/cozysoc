package storage

import (
	"context"
	"database/sql"
)

// evidenceBatchCandidate contains bounded SQL routing metadata. Original records
// are usable only after every selected-payload/index check succeeds.
type evidenceBatchCandidate struct {
	ID, SourceID, GroupID                              int64
	Entries                                            int
	NextExpiry, FirstClaim, LastClaim, LastClaimExpiry int64
	Data                                               []byte
	Sensor, Stream                                     string
}

func (b evidenceBatchCandidate) records(ctx context.Context, tx *sql.Tx, scope string) ([]EvidenceBatchRecord, error) {
	records, err := DecodeEvidenceBatch(b.Data)
	if err != nil {
		return nil, err
	}
	if len(records) != b.Entries || nextEvidenceBatchExpiry(records) != b.NextExpiry {
		return nil, ErrEvidenceBatchData
	}
	if err := validateEvidenceBatchClaimTimes(records); err != nil {
		return nil, err
	}
	first, last, expiry := evidenceBatchClaimBounds(records)
	if first != b.FirstClaim || last != b.LastClaim || expiry != b.LastClaimExpiry {
		return nil, ErrEvidenceBatchData
	}
	for _, record := range records {
		if err := validateRetainedBatchBundle(record, scope, b.Sensor, b.Stream); err != nil {
			return nil, err
		}
	}
	if err := validateEvidenceBatchIdentityGroup(ctx, tx, b.GroupID, b.SourceID, records); err != nil {
		return nil, err
	}
	if err := validateEvidenceBatchLookups(ctx, tx, b.ID, b.SourceID, records); err != nil {
		return nil, err
	}
	return records, nil
}
