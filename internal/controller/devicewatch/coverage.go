package devicewatch

import (
	"context"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

const (
	coverageFreshnessWindow = 3 * time.Minute
	coverageFutureTolerance = time.Minute
)

type coverageSampleReader interface {
	LatestCoverageSample(context.Context, string, string) (domain.CoverageSample, bool, error)
}

func coverageVerificationSignal(ctx context.Context, reader coverageSampleReader, scopeID string, now time.Time) (capability.VerificationSignal, error) {
	signal := capability.VerificationSignal{
		ID:      "observation-freshness",
		Status:  capability.SignalMissing,
		Message: "no retained Device Watch coverage sample is available",
	}
	if reader == nil {
		return signal, nil
	}

	sample, ok, err := reader.LatestCoverageSample(ctx, scopeID, CapabilityID)
	if err != nil {
		return capability.VerificationSignal{}, err
	}
	if !ok {
		return signal, nil
	}

	now = now.UTC()
	endedAt := sample.EndedAt.UTC()
	if endedAt.After(now.Add(coverageFutureTolerance)) {
		signal.Status = capability.SignalFailed
		signal.Message = "latest Device Watch coverage sample is ahead of the controller clock"
		return signal, nil
	}
	if now.Sub(endedAt) > coverageFreshnessWindow {
		signal.Status = capability.SignalStale
		signal.Message = "latest Device Watch coverage sample is older than the freshness window"
		return signal, nil
	}

	switch sample.Status {
	case "partial":
		signal.Status = capability.SignalFresh
		signal.Message = "fresh passive neighbor-cache coverage evidence is available"
	case "unavailable":
		signal.Status = capability.SignalFailed
		signal.Message = "current passive neighbor sources are unavailable"
	default:
		signal.Status = capability.SignalFailed
		signal.Message = "latest Device Watch coverage sample has an unsupported status"
	}
	return signal, nil
}
