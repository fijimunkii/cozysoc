export type CoverageState =
  | "unconfigured"
  | "unavailable"
  | "permission-required"
  | "unverified"
  | "active-limited"
  | "degraded"
  | "stale"
  | "disconnected";

export type CoverageSourceState =
  | "expected-unverified"
  | "current"
  | "permission-required"
  | "unavailable"
  | "degraded"
  | "stale"
  | "disconnected"
  | "unknown";

export type CoverageDimensionKind =
  | "network"
  | "interface"
  | "vlan"
  | "device"
  | "address-family"
  | "wireless-band"
  | "wireless-channel";

export type CoverageDirection =
  | "ingress"
  | "egress"
  | "east-west"
  | "client-to-service"
  | "service-to-client";

export type CoverageCadenceMode = "unknown" | "continuous" | "periodic" | "event-driven" | "hopping";

export interface CoverageDimension {
  kind: CoverageDimensionKind;
  value: string;
}

export interface CoverageScope {
  configured: CoverageDimension[];
  verified: CoverageDimension[];
  expected_unverified: CoverageDimension[];
}

export interface CoverageSource {
  id: string;
  kind: string;
  state: CoverageSourceState;
  expected: boolean;
  observed: boolean;
  next_step?: string;
}

export interface CoverageEvidenceWindow {
  has_evidence: boolean;
  started_at?: string;
  ended_at?: string;
  fresh_until?: string;
}

export interface CoverageCadence {
  mode: CoverageCadenceMode;
  interval_ms?: number;
  dwell_ms?: number;
}

export interface CoverageGap {
  id: string;
  kind: string;
  summary: string;
  detail: string;
  next_step: string;
  dimensions: CoverageDimension[];
  directions: CoverageDirection[];
}

export interface CoverageObservationPoint {
  id: string;
  kind: string;
  sensor_id?: string;
  state: CoverageState;
  reason: string;
  scope: CoverageScope;
  sources: CoverageSource[];
  directions: CoverageDirection[];
  window: CoverageEvidenceWindow;
  cadence: CoverageCadence;
  gaps: CoverageGap[];
  next_step: string;
}

export interface CoverageReport {
  capability_id: string;
  configured: boolean;
  state: CoverageState;
  reason: string;
  observation_points: CoverageObservationPoint[];
  next_step: string;
}
