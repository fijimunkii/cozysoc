import type { CoverageCadence, CoverageDimension, CoverageObservationPoint, CoverageState } from "./types";

const statePresentation: Record<CoverageState, { label: string; description: string }> = {
  unconfigured: { label: "Not configured", description: "This capability has not been configured yet." },
  unavailable: { label: "Unavailable", description: "This observation capability is not available from the current setup." },
  "permission-required": { label: "Permission needed", description: "A required permission is not currently available." },
  unverified: { label: "Not verified yet", description: "Configured scope exists, but current evidence has not verified it yet." },
  "active-limited": { label: "Active, with limits", description: "Current evidence is arriving, with observation boundaries kept visible below." },
  degraded: { label: "Needs attention", description: "Current evidence or its collection path has a problem that limits what can be trusted right now." },
  stale: { label: "Evidence is stale", description: "The latest trusted evidence is too old to establish current visibility." },
  disconnected: { label: "Disconnected", description: "The configured observation point is not currently connected to its evidence path." },
};

const dimensionLabels: Record<CoverageDimension["kind"], string> = {
  network: "Network",
  interface: "Interface",
  vlan: "VLAN",
  device: "Device",
  "address-family": "Address family",
  "wireless-band": "Wireless band",
  "wireless-channel": "Wireless channel",
};

const pointTitles: Record<string, string> = {
  "host-neighbor-cache": "Local device visibility",
  "dns-resolver": "DNS resolver visibility",
  "gateway-packet-sensor": "Gateway traffic visibility",
  "host-packet-sensor": "This device's traffic visibility",
  "wireless-radio": "Wireless radio visibility",
};

const capabilityTitles: Record<string, string> = {
  "device-watch": "Device visibility",
  "dns-protection": "DNS visibility",
  "traffic-watch": "Traffic visibility",
  "wireless-watch": "Wireless visibility",
};

export function coverageStatePresentation(state: CoverageState) {
  return statePresentation[state];
}

export function coverageCapabilityTitle(capabilityID: string): string {
  return capabilityTitles[capabilityID] ?? humanizeToken(capabilityID);
}

export function coveragePointTitle(point: CoverageObservationPoint): string {
  return pointTitles[point.kind] ?? humanizeToken(point.kind);
}

export function coverageDimensionPresentation(dimension: CoverageDimension): { label: string; value: string } {
  return {
    label: dimensionLabels[dimension.kind],
    value: humanizeDimensionValue(dimension),
  };
}

export function coverageCadencePresentation(cadence: CoverageCadence): string {
  switch (cadence.mode) {
    case "continuous":
      return "Continuous observation";
    case "event-driven":
      return "Updates when source events arrive";
    case "periodic":
      return cadence.interval_ms === undefined ? "Periodic observation" : `Samples about every ${formatDuration(cadence.interval_ms)}`;
    case "hopping":
      return cadence.dwell_ms === undefined ? "Hopping observation" : `Hops between configured targets with ${formatDuration(cadence.dwell_ms)} dwell`;
    case "unknown":
      return "Observation cadence not established";
  }
}

export function coverageEvidencePresentation(point: CoverageObservationPoint): string {
  if (!point.window.has_evidence || point.window.ended_at === undefined) return "No trusted evidence timestamp is available yet.";
  const ended = formatTimestamp(point.window.ended_at);
  if (point.window.fresh_until === undefined) return `Latest trusted evidence: ${ended}.`;
  return `Latest trusted evidence: ${ended}. Freshness boundary: ${formatTimestamp(point.window.fresh_until)}.`;
}

function humanizeDimensionValue(dimension: CoverageDimension): string {
  if (dimension.kind === "address-family") return dimension.value.toUpperCase();
  if (dimension.kind === "wireless-band") return dimension.value.replace("-ghz", " GHz");
  return dimension.value;
}

function humanizeToken(value: string): string {
  const words = value.replace(/[._-]+/g, " ").trim();
  if (words.length === 0) return value;
  return words.charAt(0).toUpperCase() + words.slice(1);
}

function formatDuration(milliseconds: number): string {
  if (milliseconds % 60_000 === 0) {
    const minutes = milliseconds / 60_000;
    return `${minutes} ${minutes === 1 ? "minute" : "minutes"}`;
  }
  if (milliseconds % 1_000 === 0) {
    const seconds = milliseconds / 1_000;
    return `${seconds} ${seconds === 1 ? "second" : "seconds"}`;
  }
  return `${milliseconds} ms`;
}

function formatTimestamp(value: string): string {
  return new Intl.DateTimeFormat("en", {
    dateStyle: "medium",
    timeStyle: "short",
    timeZone: "UTC",
  }).format(new Date(value));
}
