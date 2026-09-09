package devicewatch

func EffectiveCoverage(report CoverageReport, operational OperationalHealth) (CoverageState, string, string) {
	switch operational.Sensor.State {
	case OperationalDisconnected:
		return CoverageDisconnected, "sensor-disconnected", operational.Sensor.NextStep
	case OperationalStale:
		return CoverageStale, "sensor-stale", operational.Sensor.NextStep
	case OperationalDegraded, OperationalUnavailable:
		return CoverageDegraded, "sensor-degraded", operational.Sensor.NextStep
	case OperationalStarting:
		return CoverageUnverified, "sensor-starting", operational.Sensor.NextStep
	}
	if operational.Pipeline.State != OperationalCurrent {
		if operational.Pipeline.Reason == "sqlite-full" && operational.Database.State != OperationalCurrent {
			return CoverageDegraded, "storage-" + operational.Database.Reason, operational.Database.NextStep
		}
		return CoverageDegraded, "ingestion-" + operational.Pipeline.Reason, operational.Pipeline.NextStep
	}
	if operational.Database.State != OperationalCurrent {
		return CoverageDegraded, "storage-" + operational.Database.Reason, operational.Database.NextStep
	}
	return report.State, report.Reason, report.NextStep
}
