import { DeviceLoadError, parseDevicePresence, type DevicePresence } from "./devices";

const maxEvidence = 100;
const idPattern = /^[a-z][a-z0-9._:-]{0,127}$/;
const tokenPattern = /^[a-z0-9][a-z0-9._:-]*$/;
const controlCharacters = /[\u0000-\u001f\u007f]/;
const claimKinds = ["ipv4", "ipv6", "mac", "hostname", "service-name", "dhcp-client-id", "endpoint-id"] as const;

export type IdentityClaimKind = typeof claimKinds[number];
export type EvidenceAuthority = "inferred" | "user";

export interface DeviceEvidenceSource {
  observation_id: string;
  sensor_id: string;
  kind: string;
  source_stream: string;
  ingested_at: string;
  attribution: string;
}

export interface DeviceIdentityEvidence {
  kind: IdentityClaimKind;
  value: string;
  observed_at: string;
  valid_until?: string;
  link_valid_until?: string;
  current: boolean;
  claim_confidence?: number;
  link_confidence?: number;
  authority: EvidenceAuthority;
  reason: string;
  source_sensor_id: string;
  source?: DeviceEvidenceSource;
}

export interface DeviceDetail {
  scope_id: string;
  as_of: string;
  device: DevicePresence;
  evidence: DeviceIdentityEvidence[];
  truncated: boolean;
}

export function parseDeviceDetail(input: unknown): DeviceDetail {
  const value = objectValue(input, "device detail");
  const scopeID = idValue(value.scope_id, "scope_id");
  const asOf = timestampValue(value.as_of, "as_of");
  const device = parseDevicePresence(value.device);
  if (Date.parse(device.last_seen) > Date.parse(asOf)) throw new DeviceLoadError("device detail last_seen is after as_of");
  if (!Array.isArray(value.evidence) || value.evidence.length > maxEvidence) throw new DeviceLoadError("device detail has an invalid evidence collection");
  if (typeof value.truncated !== "boolean") throw new DeviceLoadError("device detail has an invalid truncated flag");
  const evidence = value.evidence.map((item) => parseEvidence(item, asOf));
  return { scope_id: scopeID, as_of: asOf, device, evidence, truncated: value.truncated };
}

export async function loadDeviceDetailFromWeb(deviceID: string): Promise<DeviceDetail> {
  if (!idPattern.test(deviceID)) throw new DeviceLoadError("Device id is invalid. Refresh the device list and try again.");
  let response: Response;
  try {
    const query = new URLSearchParams({ device_id: deviceID });
    response = await fetch(`/api/devices/detail?${query.toString()}`, { method: "GET", headers: { Accept: "application/json" }, credentials: "same-origin", cache: "no-store" });
  } catch {
    throw new DeviceLoadError("Unable to reach the local Cozy SOC web service.");
  }
  if (response.status === 401) throw new DeviceLoadError("The local web session expired. Reopen the authenticated Cozy SOC URL and try again.");
  if (response.status === 404) throw new DeviceLoadError("This device is no longer available in the current authorized scope. Refresh the device list and try again.");
  if (!response.ok) throw new DeviceLoadError(`Live device evidence request failed with status ${response.status}.`);
  const contentType = response.headers.get("content-type") ?? "";
  if (!contentType.toLowerCase().startsWith("application/json")) throw new DeviceLoadError("Live device evidence response was not JSON.");
  let payload: unknown;
  try { payload = await response.json(); } catch { throw new DeviceLoadError("Live device evidence response was not valid JSON."); }
  const detail = parseDeviceDetail(payload);
  if (detail.device.id !== deviceID) throw new DeviceLoadError("Live device evidence response did not match the requested device.");
  return detail;
}

function parseEvidence(input: unknown, asOf: string): DeviceIdentityEvidence {
  const value = objectValue(input, "identity evidence");
  if (typeof value.kind !== "string" || !(claimKinds as readonly string[]).includes(value.kind)) throw new DeviceLoadError("identity evidence has an invalid kind");
  const claimValue = boundedText(value.value, "identity evidence value", 512);
  const observedAt = timestampValue(value.observed_at, "identity evidence observed_at");
  if (Date.parse(observedAt) > Date.parse(asOf)) throw new DeviceLoadError("identity evidence is newer than device detail as_of");
  const validUntil = optionalTimestamp(value.valid_until, "identity evidence valid_until");
  if (validUntil !== undefined && Date.parse(validUntil) < Date.parse(observedAt)) throw new DeviceLoadError("identity evidence valid_until precedes observed_at");
  const linkValidUntil = optionalTimestamp(value.link_valid_until, "identity evidence link_valid_until");
  if (linkValidUntil !== undefined && Date.parse(linkValidUntil) < Date.parse(observedAt)) throw new DeviceLoadError("identity evidence link_valid_until precedes observed_at");
  if (typeof value.current !== "boolean") throw new DeviceLoadError("identity evidence has an invalid current flag");
  if (value.authority !== "inferred" && value.authority !== "user") throw new DeviceLoadError("identity evidence has an invalid authority");
  const reason = boundedText(value.reason, "identity evidence reason", 512);
  const sourceSensorID = idValue(value.source_sensor_id, "identity evidence source_sensor_id");
  const claimConfidence = optionalConfidence(value.claim_confidence, "claim_confidence");
  const linkConfidence = optionalConfidence(value.link_confidence, "link_confidence");
  const result: DeviceIdentityEvidence = { kind: value.kind as IdentityClaimKind, value: claimValue, observed_at: observedAt, current: value.current, authority: value.authority, reason, source_sensor_id: sourceSensorID };
  if (validUntil !== undefined) result.valid_until = validUntil;
  if (linkValidUntil !== undefined) result.link_valid_until = linkValidUntil;
  if (claimConfidence !== undefined) result.claim_confidence = claimConfidence;
  if (linkConfidence !== undefined) result.link_confidence = linkConfidence;
  if (value.source !== undefined) result.source = parseSource(value.source);
  return result;
}

function parseSource(input: unknown): DeviceEvidenceSource {
  const value = objectValue(input, "evidence source");
  const kind = tokenValue(value.kind, "evidence source kind", 96);
  return {
    observation_id: idValue(value.observation_id, "evidence source observation_id"),
    sensor_id: idValue(value.sensor_id, "evidence source sensor_id"),
    kind,
    source_stream: boundedText(value.source_stream, "evidence source stream", 128),
    ingested_at: timestampValue(value.ingested_at, "evidence source ingested_at"),
    attribution: boundedText(value.attribution, "evidence source attribution", 256),
  };
}

function objectValue(input: unknown, label: string): Record<string, unknown> {
  if (typeof input !== "object" || input === null || Array.isArray(input)) throw new DeviceLoadError(`${label} must be an object`);
  return input as Record<string, unknown>;
}

function idValue(input: unknown, label: string): string {
  if (typeof input !== "string" || !idPattern.test(input)) throw new DeviceLoadError(`${label} is invalid`);
  return input;
}

function tokenValue(input: unknown, label: string, maxBytes: number): string {
  if (typeof input !== "string" || !tokenPattern.test(input) || utf8Bytes(input) > maxBytes) throw new DeviceLoadError(`${label} is invalid`);
  return input;
}

function boundedText(input: unknown, label: string, maxBytes: number): string {
  if (typeof input !== "string" || input.length === 0 || input.trim() !== input || controlCharacters.test(input) || utf8Bytes(input) > maxBytes) throw new DeviceLoadError(`${label} is invalid`);
  return input;
}

function timestampValue(input: unknown, label: string): string {
  if (typeof input !== "string" || input.length === 0 || input.length > 64 || Number.isNaN(Date.parse(input))) throw new DeviceLoadError(`${label} is invalid`);
  return input;
}

function optionalTimestamp(input: unknown, label: string): string | undefined {
  return input === undefined ? undefined : timestampValue(input, label);
}

function optionalConfidence(input: unknown, label: string): number | undefined {
  if (input === undefined) return undefined;
  if (typeof input !== "number" || !Number.isFinite(input) || input < 0 || input > 1) throw new DeviceLoadError(`${label} is invalid`);
  return input;
}

function utf8Bytes(value: string): number { return new TextEncoder().encode(value).length; }
