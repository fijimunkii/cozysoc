const maxActivityItems = 100;
const identifierPattern = /^[a-z][a-z0-9._:-]{0,127}$/;
const macPattern = /^[0-9a-f]{2}(?::[0-9a-f]{2}){5}$/;
const maxTimestampLength = 64;
const controlCharacters = /[\u0000-\u001f\u007f]/;

export type DeviceActivityKind = "first-observed" | "address-changed" | "observed";

export interface DeviceActivitySource {
  observation_id: string;
  sensor_id: string;
  kind: "device-neighbor-seen";
  source_stream: "device-watch-neighbors";
  ingested_at: string;
  attribution: "device-watch:arp-cache" | "device-watch:ndp-cache";
}

export interface DeviceActivityItem {
  id: string;
  kind: DeviceActivityKind;
  at: string;
  device_id: string;
  user_label?: string;
  address_family: "ipv4" | "ipv6";
  address: string;
  previous_address?: string;
  hardware_address: string;
  source: DeviceActivitySource;
}

export interface DeviceActivityList {
  configured: boolean;
  scope_id?: string;
  since: string;
  as_of: string;
  items: DeviceActivityItem[];
  truncated: boolean;
}

export class ActivityLoadError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ActivityLoadError";
  }
}

export function parseDeviceActivity(input: unknown): DeviceActivityList {
  const value = objectValue(input, "activity response");
  if (typeof value.configured !== "boolean") throw new ActivityLoadError("activity response has an invalid configured flag");
  if (typeof value.truncated !== "boolean") throw new ActivityLoadError("activity response has an invalid truncated flag");
  const since = timestampValue(value.since, "activity since");
  const asOf = timestampValue(value.as_of, "activity as_of");
  if (Date.parse(since) > Date.parse(asOf)) throw new ActivityLoadError("activity window is invalid");
  if (!Array.isArray(value.items) || value.items.length > maxActivityItems) throw new ActivityLoadError("activity response has an invalid item collection");
  const scopeID = optionalIdentifier(value.scope_id, "scope_id");
  if (value.configured && scopeID === undefined) throw new ActivityLoadError("configured activity response is missing scope_id");
  if (!value.configured && scopeID !== undefined) throw new ActivityLoadError("unconfigured activity response cannot carry scope_id");
  if (!value.configured && value.items.length !== 0) throw new ActivityLoadError("unconfigured activity response cannot carry items");

  const seen = new Set<string>();
  const items = value.items.map((item) => parseItem(item, since, asOf));
  for (let index = 1; index < items.length; index++) {
    const previous = items[index - 1];
    const current = items[index];
    if (previous !== undefined && current !== undefined && Date.parse(previous.at) < Date.parse(current.at)) {
      throw new ActivityLoadError("activity items are not newest-first");
    }
  }
  for (const item of items) {
    if (seen.has(item.id)) throw new ActivityLoadError(`duplicate activity item ${item.id}`);
    seen.add(item.id);
  }
  const result: DeviceActivityList = { configured: value.configured, since, as_of: asOf, items, truncated: value.truncated };
  if (scopeID !== undefined) result.scope_id = scopeID;
  return result;
}

export async function loadDeviceActivityFromWeb(): Promise<DeviceActivityList> {
  let response: Response;
  try {
    response = await fetch("/api/activity", { method: "GET", headers: { Accept: "application/json" }, credentials: "same-origin", cache: "no-store" });
  } catch {
    throw new ActivityLoadError("Unable to reach the local Cozy SOC web service.");
  }
  if (response.status === 401) throw new ActivityLoadError("Open the authenticated local URL printed by `cozysoc web`, then retry.");
  if (!response.ok) throw new ActivityLoadError(`Live activity request failed with status ${response.status}.`);
  const contentType = response.headers.get("content-type") ?? "";
  if (!contentType.toLowerCase().startsWith("application/json")) throw new ActivityLoadError("Live activity response was not JSON.");
  let payload: unknown;
  try { payload = await response.json(); } catch { throw new ActivityLoadError("Live activity response was not valid JSON."); }
  return parseDeviceActivity(payload);
}

function parseItem(input: unknown, since: string, asOf: string): DeviceActivityItem {
  const value = objectValue(input, "activity item");
  const id = identifierValue(value.id, "activity id");
  const deviceID = identifierValue(value.device_id, "activity device id");
  if (value.kind !== "first-observed" && value.kind !== "address-changed" && value.kind !== "observed") throw new ActivityLoadError(`activity ${id} has an invalid kind`);
  const at = timestampValue(value.at, `activity ${id} at`);
  if (Date.parse(at) < Date.parse(since) || Date.parse(at) > Date.parse(asOf)) throw new ActivityLoadError(`activity ${id} falls outside its declared window`);
  if (value.address_family !== "ipv4" && value.address_family !== "ipv6") throw new ActivityLoadError(`activity ${id} has an invalid address family`);
  const address = textValue(value.address, `activity ${id} address`, 64);
  if (value.address_family === "ipv4" ? address.includes(":") : !address.includes(":")) throw new ActivityLoadError(`activity ${id} address does not match its family`);
  const hardwareAddress = textValue(value.hardware_address, `activity ${id} hardware address`, 32);
  if (!macPattern.test(hardwareAddress)) throw new ActivityLoadError(`activity ${id} has an invalid hardware address`);
  const previousAddress = optionalText(value.previous_address, `activity ${id} previous address`, 64);
  if (value.kind === "address-changed") {
    if (previousAddress === undefined || previousAddress === address) throw new ActivityLoadError(`activity ${id} address change is missing a distinct previous address`);
  } else if (previousAddress !== undefined) {
    throw new ActivityLoadError(`activity ${id} carries a previous address without an address change`);
  }
  const userLabel = optionalText(value.user_label, `activity ${id} user label`, 160);
  const source = parseSource(value.source, id);
  const item: DeviceActivityItem = { id, kind: value.kind, at, device_id: deviceID, address_family: value.address_family, address, hardware_address: hardwareAddress, source };
  if (userLabel !== undefined) item.user_label = userLabel;
  if (previousAddress !== undefined) item.previous_address = previousAddress;
  return item;
}

function parseSource(input: unknown, id: string): DeviceActivitySource {
  const value = objectValue(input, `activity ${id} source`);
  const observationID = identifierValue(value.observation_id, `activity ${id} observation id`);
  if (observationID !== id) throw new ActivityLoadError(`activity ${id} source observation does not match the activity id`);
  const sensorID = identifierValue(value.sensor_id, `activity ${id} sensor id`);
  if (value.kind !== "device-neighbor-seen" || value.source_stream !== "device-watch-neighbors") throw new ActivityLoadError(`activity ${id} source contract is invalid`);
  if (value.attribution !== "device-watch:arp-cache" && value.attribution !== "device-watch:ndp-cache") throw new ActivityLoadError(`activity ${id} source attribution is invalid`);
  return { observation_id: observationID, sensor_id: sensorID, kind: value.kind, source_stream: value.source_stream, ingested_at: timestampValue(value.ingested_at, `activity ${id} ingested_at`), attribution: value.attribution };
}

function objectValue(input: unknown, field: string): Record<string, unknown> {
  if (typeof input !== "object" || input === null || Array.isArray(input)) throw new ActivityLoadError(`${field} must be an object`);
  return input as Record<string, unknown>;
}
function identifierValue(input: unknown, field: string): string {
  if (typeof input !== "string" || !identifierPattern.test(input)) throw new ActivityLoadError(`${field} is invalid`);
  return input;
}
function optionalIdentifier(input: unknown, field: string): string | undefined { return input === undefined ? undefined : identifierValue(input, field); }
function textValue(input: unknown, field: string, max: number): string {
  if (typeof input !== "string" || input.length === 0 || input.length > max || input.trim() !== input || controlCharacters.test(input)) throw new ActivityLoadError(`${field} is invalid`);
  return input;
}
function optionalText(input: unknown, field: string, max: number): string | undefined { return input === undefined ? undefined : textValue(input, field, max); }
function timestampValue(input: unknown, field: string): string {
  if (typeof input !== "string" || input.length === 0 || input.length > maxTimestampLength || Number.isNaN(Date.parse(input))) throw new ActivityLoadError(`${field} is invalid`);
  return input;
}
