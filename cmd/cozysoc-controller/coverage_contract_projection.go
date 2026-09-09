package main

import (
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	sharedcoverage "github.com/fijimunkii/cozysoc/internal/controller/coverage"
)

func projectCoverageReport(report sharedcoverage.Report, asOf time.Time) api.CoverageReport {
	result := api.CoverageReport{
		CapabilityID:      report.CapabilityID,
		Configured:        report.Configured,
		AsOf:              asOf.UTC(),
		State:             string(report.State),
		Reason:            report.Reason,
		ObservationPoints: make([]api.CoverageObservationPoint, 0, len(report.ObservationPoints)),
		NextStep:          report.NextStep,
	}
	for _, point := range report.ObservationPoints {
		projected := api.CoverageObservationPoint{
			ID:         point.ID,
			Kind:       point.Kind,
			SensorID:   point.SensorID,
			State:      string(point.State),
			Reason:     point.Reason,
			Scope:      projectCoverageScope(point.Scope),
			Sources:    make([]api.CoverageSource, 0, len(point.Sources)),
			Directions: make([]string, 0, len(point.Directions)),
			Window: api.CoverageEvidenceWindow{
				HasEvidence: point.Window.HasEvidence,
			},
			Cadence: api.CoverageCadence{
				Mode:       string(point.Cadence.Mode),
				IntervalMS: point.Cadence.Interval.Milliseconds(),
				DwellMS:    point.Cadence.Dwell.Milliseconds(),
			},
			Gaps:     make([]api.CoverageGap, 0, len(point.Gaps)),
			NextStep: point.NextStep,
		}
		if point.Window.HasEvidence {
			startedAt := point.Window.StartedAt.UTC()
			endedAt := point.Window.EndedAt.UTC()
			freshUntil := point.Window.FreshUntil.UTC()
			projected.Window.StartedAt = &startedAt
			projected.Window.EndedAt = &endedAt
			projected.Window.FreshUntil = &freshUntil
		}
		for _, source := range point.Sources {
			projected.Sources = append(projected.Sources, api.CoverageSource{
				ID:       source.ID,
				Kind:     source.Kind,
				State:    string(source.State),
				Expected: source.Expected,
				Observed: source.Observed,
				NextStep: source.NextStep,
			})
		}
		for _, direction := range point.Directions {
			projected.Directions = append(projected.Directions, string(direction))
		}
		for _, gap := range point.Gaps {
			projectedGap := api.CoverageGap{
				ID:         gap.ID,
				Kind:       gap.Kind,
				Summary:    gap.Summary,
				Detail:     gap.Detail,
				NextStep:   gap.NextStep,
				Dimensions: projectCoverageDimensions(gap.Dimensions),
				Directions: make([]string, 0, len(gap.Directions)),
			}
			for _, direction := range gap.Directions {
				projectedGap.Directions = append(projectedGap.Directions, string(direction))
			}
			projected.Gaps = append(projected.Gaps, projectedGap)
		}
		result.ObservationPoints = append(result.ObservationPoints, projected)
	}
	return result
}

func projectCoverageScope(scope sharedcoverage.Scope) api.CoverageScope {
	return api.CoverageScope{
		Configured:         projectCoverageDimensions(scope.Configured),
		Verified:           projectCoverageDimensions(scope.Verified),
		ExpectedUnverified: projectCoverageDimensions(scope.ExpectedUnverified),
	}
}

func projectCoverageDimensions(dimensions []sharedcoverage.Dimension) []api.CoverageDimension {
	result := make([]api.CoverageDimension, 0, len(dimensions))
	for _, dimension := range dimensions {
		result = append(result, api.CoverageDimension{Kind: string(dimension.Kind), Value: dimension.Value})
	}
	return result
}
