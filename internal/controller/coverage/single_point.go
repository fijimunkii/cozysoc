package coverage

import "fmt"

// ValidateSinglePointReport applies the only aggregation rule Cozy SOC can
// currently prove in production: when a capability has exactly one observation
// point, the capability aggregate must be that point's state, reason, and next
// step. Multi-point aggregation is intentionally deferred until a real producer
// supplies more than one observation point.
func ValidateSinglePointReport(report Report) error {
	if err := ValidateReport(report); err != nil {
		return err
	}
	if !report.Configured {
		return nil
	}
	if len(report.ObservationPoints) != 1 {
		return fmt.Errorf("single-point coverage validation requires exactly one observation point")
	}
	point := report.ObservationPoints[0]
	if report.State != point.State || report.Reason != point.Reason || report.NextStep != point.NextStep {
		return fmt.Errorf("single-point coverage aggregate must match its observation point")
	}
	return nil
}
