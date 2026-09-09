import type {
  CoverageCadence,
  CoverageCadenceMode,
  CoverageDimension,
  CoverageDimensionKind,
  CoverageDirection,
  CoverageEvidenceWindow,
  CoverageGap,
  CoverageObservationPoint,
  CoverageReport,
  CoverageScope,
  CoverageSource,
  CoverageSourceState,
  CoverageState,
} from "./types";

const tokenPattern = /^[a-z0-9]+(?:[._-][a-z0-9]+)*$/;
const coverageStates = new Set<CoverageState>([
  "unconfigured",
  "unavailable",
  "permission-required",
  "unverified",
  "active-limited",
  "degraded",
  "stale",
  "disconnected",
]);
const sourceStates = new Set<CoverageSourceState>([
  "expected-unverified",
  "current",
  "permission-required",
  "unavailable",
  "degraded",
  "stale",
  "disconnected",
  "unknown",
]);
const dimensionKinds = new Set<CoverageDimensionKind>([
  "network",
  "interface",
  "vlan",
  "device",
  "address-family",
  "wireless-band",
  "wireless-channel",
]);
const directions = new Set<CoverageDirection>([
  "ingress",
  "egress",
  "east-west",
  "client-to-service",
  "service-to-client",
]);
const cadenceModes = new Set<CoverageCadenceMode>(["unknown", "continuous", "periodic", "event-driven", "hopping"]);

type JsonRecord = Record<string, unknown>;

export class CoverageParseError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "CoverageParseError";
  }
}

export function parseCoverageReport(input: unknown): CoverageReport {
  const value = record(input, "coverage");
  const report: CoverageReport = {
    capability_id: token(value, "capability_id", "coverage"),
    configured: booleanValue(value, "configured", "coverage"),
    state: enumValue(value, "state", "coverage", coverageStates),
    reason: token(value, "reason", "coverage"),
    observation_points: array(value, "observation_points", "coverage", 32).map((point, index) =>
      parseObservationPoint(point, `coverage.observation_points[${index}]`),
    ),
    next_step: text(value, "next_step", "coverage", 512),
  };

  if (!report.configured) {
    if (report.state !== "unconfigured" || report.observation_points.length !== 0) {
      fail("unconfigured coverage cannot claim observation points or another state");
    }
    return report;
  }
  if (report.state === "unconfigured" || report.observation_points.length === 0) {
    fail("configured coverage requires at least one configured observation point");
  }
  if (report.observation_points.length === 1) {
    const point = report.observation_points[0];
    if (point === undefined || point.state !== report.state || point.reason !== report.reason || point.next_step !== report.next_step) {
      fail("single-point coverage aggregate must match its observation point");
    }
  }
  return report;
}

function parseObservationPoint(input: unknown, path: string): CoverageObservationPoint {
  const value = record(input, path);
  const point: CoverageObservationPoint = {
    id: token(value, "id", path),
    kind: token(value, "kind", path),
    state: enumValue(value, "state", path, coverageStates),
    reason: token(value, "reason", path),
    scope: parseScope(value.scope, `${path}.scope`),
    sources: array(value, "sources", path, 32).map((source, index) => parseSource(source, `${path}.sources[${index}]`)),
    directions: array(value, "directions", path, 8).map((direction, index) =>
      enumInput(direction, `${path}.directions[${index}]`, directions),
    ),
    window: parseWindow(value.window, `${path}.window`),
    cadence: parseCadence(value.cadence, `${path}.cadence`),
    gaps: array(value, "gaps", path, 32).map((gap, index) => parseGap(gap, `${path}.gaps[${index}]`)),
    next_step: text(value, "next_step", path, 512),
  };
  const sensorID = optionalToken(value, "sensor_id", path);
  if (sensorID !== undefined) {
    point.sensor_id = sensorID;
  }
  if (point.state === "unconfigured") {
    fail(`${path}.state cannot be unconfigured for a configured observation point`);
  }
  uniqueBy(point.sources, (source) => source.id, `${path}.sources`);
  uniqueBy(point.gaps, (gap) => gap.id, `${path}.gaps`);
  uniqueStrings(point.directions, `${path}.directions`);
  return point;
}

function parseScope(input: unknown, path: string): CoverageScope {
  const value = record(input, path);
  const scope: CoverageScope = {
    configured: array(value, "configured", path, 128).map((dimension, index) =>
      parseDimension(dimension, `${path}.configured[${index}]`),
    ),
    verified: array(value, "verified", path, 128).map((dimension, index) =>
      parseDimension(dimension, `${path}.verified[${index}]`),
    ),
    expected_unverified: array(value, "expected_unverified", path, 128).map((dimension, index) =>
      parseDimension(dimension, `${path}.expected_unverified[${index}]`),
    ),
  };
  const configured = dimensionKeys(scope.configured, `${path}.configured`);
  const verified = dimensionKeys(scope.verified, `${path}.verified`);
  for (const dimension of scope.expected_unverified) {
    const key = dimensionKey(dimension);
    if (!configured.has(key)) {
      fail(`${path}.expected_unverified contains a dimension outside configured scope`);
    }
    if (verified.has(key)) {
      fail(`${path} cannot mark one dimension both verified and expected-unverified`);
    }
  }
  return scope;
}

function parseDimension(input: unknown, path: string): CoverageDimension {
  const value = record(input, path);
  return {
    kind: enumValue(value, "kind", path, dimensionKinds),
    value: text(value, "value", path, 256),
  };
}

function parseSource(input: unknown, path: string): CoverageSource {
  const value = record(input, path);
  const source: CoverageSource = {
    id: token(value, "id", path),
    kind: token(value, "kind", path),
    state: enumValue(value, "state", path, sourceStates),
    expected: booleanValue(value, "expected", path),
    observed: booleanValue(value, "observed", path),
  };
  const nextStep = optionalText(value, "next_step", path, 512);
  if (nextStep !== undefined) {
    source.next_step = nextStep;
  }
  return source;
}

function parseWindow(input: unknown, path: string): CoverageEvidenceWindow {
  const value = record(input, path);
  const hasEvidence = booleanValue(value, "has_evidence", path);
  const startedAt = optionalTimestamp(value, "started_at", path);
  const endedAt = optionalTimestamp(value, "ended_at", path);
  const freshUntil = optionalTimestamp(value, "fresh_until", path);
  if (!hasEvidence) {
    if (startedAt !== undefined || endedAt !== undefined || freshUntil !== undefined) {
      fail(`${path} cannot contain evidence timestamps when has_evidence is false`);
    }
    return { has_evidence: false };
  }
  if (startedAt === undefined || endedAt === undefined || freshUntil === undefined) {
    fail(`${path} requires started_at, ended_at, and fresh_until when evidence exists`);
  }
  if (Date.parse(endedAt) < Date.parse(startedAt) || Date.parse(freshUntil) < Date.parse(endedAt)) {
    fail(`${path} contains an inconsistent evidence window`);
  }
  return { has_evidence: true, started_at: startedAt, ended_at: endedAt, fresh_until: freshUntil };
}

function parseCadence(input: unknown, path: string): CoverageCadence {
  const value = record(input, path);
  const mode = enumValue(value, "mode", path, cadenceModes);
  const interval = optionalNonNegativeInteger(value, "interval_ms", path);
  const dwell = optionalNonNegativeInteger(value, "dwell_ms", path);
  if (mode === "periodic" && (interval === undefined || interval <= 0 || dwell !== undefined)) {
    fail(`${path} periodic cadence requires a positive interval_ms and no dwell_ms`);
  }
  if (mode === "hopping" && (dwell === undefined || dwell <= 0 || (interval !== undefined && interval < dwell))) {
    fail(`${path} hopping cadence requires positive dwell_ms and a non-shorter interval_ms when supplied`);
  }
  if ((mode === "unknown" || mode === "continuous" || mode === "event-driven") && (interval !== undefined || dwell !== undefined)) {
    fail(`${path} ${mode} cadence cannot carry interval_ms or dwell_ms`);
  }
  const cadence: CoverageCadence = { mode };
  if (interval !== undefined) cadence.interval_ms = interval;
  if (dwell !== undefined) cadence.dwell_ms = dwell;
  return cadence;
}

function parseGap(input: unknown, path: string): CoverageGap {
  const value = record(input, path);
  return {
    id: token(value, "id", path),
    kind: token(value, "kind", path),
    summary: text(value, "summary", path, 256),
    detail: text(value, "detail", path, 1024),
    next_step: text(value, "next_step", path, 512),
    dimensions: array(value, "dimensions", path, 128).map((dimension, index) =>
      parseDimension(dimension, `${path}.dimensions[${index}]`),
    ),
    directions: array(value, "directions", path, 8).map((direction, index) =>
      enumInput(direction, `${path}.directions[${index}]`, directions),
    ),
  };
}

function record(input: unknown, path: string): JsonRecord {
  if (typeof input !== "object" || input === null || Array.isArray(input)) fail(`${path} must be an object`);
  return input as JsonRecord;
}

function array(value: JsonRecord, key: string, path: string, max: number): unknown[] {
  const input = value[key];
  if (!Array.isArray(input) || input.length > max) fail(`${path}.${key} must be an array with at most ${max} items`);
  return input;
}

function booleanValue(value: JsonRecord, key: string, path: string): boolean {
  const input = value[key];
  if (typeof input !== "boolean") fail(`${path}.${key} must be a boolean`);
  return input;
}

function text(value: JsonRecord, key: string, path: string, max: number): string {
  const input = value[key];
  if (typeof input !== "string" || input.length === 0 || input.length > max || input.trim() !== input || /[\u0000\r]/.test(input)) {
    fail(`${path}.${key} must be bounded, non-empty text`);
  }
  return input;
}

function optionalText(value: JsonRecord, key: string, path: string, max: number): string | undefined {
  if (value[key] === undefined) return undefined;
  return text(value, key, path, max);
}

function token(value: JsonRecord, key: string, path: string): string {
  const input = text(value, key, path, 256);
  if (!tokenPattern.test(input)) fail(`${path}.${key} must be a bounded token`);
  return input;
}

function optionalToken(value: JsonRecord, key: string, path: string): string | undefined {
  if (value[key] === undefined) return undefined;
  return token(value, key, path);
}

function enumValue<T extends string>(value: JsonRecord, key: string, path: string, allowed: ReadonlySet<T>): T {
  return enumInput(value[key], `${path}.${key}`, allowed);
}

function enumInput<T extends string>(input: unknown, path: string, allowed: ReadonlySet<T>): T {
  if (typeof input !== "string" || !allowed.has(input as T)) fail(`${path} contains an unsupported value`);
  return input as T;
}

function optionalTimestamp(value: JsonRecord, key: string, path: string): string | undefined {
  if (value[key] === undefined) return undefined;
  const input = text(value, key, path, 64);
  if (Number.isNaN(Date.parse(input))) fail(`${path}.${key} must be an RFC3339-compatible timestamp`);
  return input;
}

function optionalNonNegativeInteger(value: JsonRecord, key: string, path: string): number | undefined {
  const input = value[key];
  if (input === undefined) return undefined;
  if (typeof input !== "number" || !Number.isSafeInteger(input) || input < 0) fail(`${path}.${key} must be a non-negative integer`);
  return input;
}

function dimensionKeys(dimensions: CoverageDimension[], path: string): Set<string> {
  const keys = new Set<string>();
  for (const dimension of dimensions) {
    const key = dimensionKey(dimension);
    if (keys.has(key)) fail(`${path} contains a duplicate dimension`);
    keys.add(key);
  }
  return keys;
}

function dimensionKey(dimension: CoverageDimension): string {
  return `${dimension.kind}\u0000${dimension.value}`;
}

function uniqueBy<T>(values: T[], key: (value: T) => string, path: string): void {
  const seen = new Set<string>();
  for (const value of values) {
    const current = key(value);
    if (seen.has(current)) fail(`${path} contains duplicate identifiers`);
    seen.add(current);
  }
}

function uniqueStrings(values: string[], path: string): void {
  if (new Set(values).size !== values.length) fail(`${path} contains duplicate values`);
}

function fail(message: string): never {
  throw new CoverageParseError(message);
}
