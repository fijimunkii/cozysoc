package devicewatch

import (
	"context"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
)

const (
	coverageFreshnessWindow = 3 * time.Minute
	coverageFutureTolerance = time.Minute
)

func coverageVerificationSignal(ctx context.Context, reader CoverageSampleReader, scopeID string, now time.Time) (capability.VerificationSignal, error) {
	report, err := CurrentCoverage(ctx, reader, scopeID, now)
	if err != nil {
		return capability.VerificationSignal{}, err
	}

	signal := capability.VerificationSignal{ID: "observation-freshness"}
	switch report.State {
	case CoverageActiveLimited:
		signal.Status = capability.SignalFresh
		signal.Message = "fresh passive neighbor-cache coverage evidence is available"
	case CoverageStale:
		signal.Status = capability.SignalStale
		signal.Message = "latest Device Watch coverage sample is older than the freshness window"
	case CoverageDegraded:
		signal.Status = capability.SignalFailed
		switch report.Reason {
		case "source-partial":
			signal.Message = "one or more current passive neighbor sources are unavailable"
		case "source-unavailable":
			signal.Message = "current passive neighbor sources are unavailable"
		case "clock-skew":
			signal.Message = "latest Device Watch coverage sample is ahead of the controller clock"
		default:
			signal.Message = "latest Device Watch coverage evidence is invalid"
		}
	default:
		signal.Status = capability.SignalMissing
		signal.Message = "no retained Device Watch coverage sample is available"
	}
	return signal, nil
}
