const maxDevices = 4096;
const maxIdentifierLength = 128;
const maxLabelLength = 160;
const maxTimestampLength = 64;
const controlCharacters = /[\u0000-\u001f\u007f]/;
const identifierPattern = /^[a-z][a-z0-9._:-]{0,127}$/;

export type DevicePresenceState = "visible" | "uncertain";

export interface DevicePresence {
  id: string;
  user_label?: string;
  first_seen: string;
  last_seen: string;
  state: DevicePresenceState;
}

export interface DeviceList {
  configured: boolean;
  scope_id?: string;
  as_of: string;
  devices: DevicePresence[];
  truncated: boolean;
}

export class DeviceLoadError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "DeviceLoadError";
  }
}

export function parseDeviceList(input: unknown): DeviceList {
  const value = objectValue(input, "device response");
  if (typeof value.configured !== "boolean") throw new DeviceLoadError("device response has an invalid configured flag");
  if (typeof value.truncated !== "boolean") throw new DeviceLoadError("device response has an invalid truncated flag");
  const asOf = timestampValue(value.as_of, "device response as_of");
  if (!Array.isArray(value.devices) || value.devices.length > maxDevices) {
    throw new DeviceLoadError("device response has an invalid device collection");
  }

  const scopeID = optionalIdentifier(value.scope_id, "scope_id");
  if (value.configured && scopeID === undefined) throw new DeviceLoadError("configured device response is missing scope_id");
  if (!value.configured && scopeID !== undefined) throw new DeviceLoadError("unconfigured device response cannot carry scope_id");

  const devices = value.devices.map(parseDevice);
  if (!value.configured && devices.length !== 0) throw new DeviceLoadError("unconfigured device response cannot carry devices");
  const seen = new Set<string>();
  for (const device of devices) {
    if (seen.has(device.id)) throw new DeviceLoadError(`duplicate device ${device.id}`);
    seen.add(device.id);
  }

  const result: DeviceList = {
    configured: value.configured,
    as_of: asOf,
    devices,
    truncated: value.truncated,
  };
  if (scopeID !== undefined) result.scope_id = scopeID;
  return result;
}

export async function loadDevicesFromWeb(): Promise<DeviceList> {
  let response: Response;
  try {
    response = await fetch("/api/devices", {
      method: "GET",
      headers: { Accept: "application/json" },
      credentials: "same-origin",
      cache: "no-store",
    });
  } catch {
    throw new DeviceLoadError("Unable to reach the local Cozy SOC web service.");
  }
  if (response.status === 401) {
    throw new DeviceLoadError("Open the authenticated local URL printed by `cozysoc web`, then retry.");
  }
  if (!response.ok) throw new DeviceLoadError(`Live device request failed with status ${response.status}.`);
  const contentType = response.headers.get("content-type") ?? "";
  if (!contentType.toLowerCase().startsWith("application/json")) {
    throw new DeviceLoadError("Live device response was not JSON.");
  }
  let payload: unknown;
  try {
    payload = await response.json();
  } catch {
    throw new DeviceLoadError("Live device response was not valid JSON.");
  }
  return parseDeviceList(payload);
}

function parseDevice(input: unknown): DevicePresence {
  const value = objectValue(input, "device");
  const id = identifierValue(value.id, "device id");
  const firstSeen = timestampValue(value.first_seen, `device ${id} first_seen`);
  const lastSeen = timestampValue(value.last_seen, `device ${id} last_seen`);
  if (Date.parse(firstSeen) > Date.parse(lastSeen)) throw new DeviceLoadError(`device ${id} first_seen is after last_seen`);
  if (value.state !== "visible" && value.state !== "uncertain") throw new DeviceLoadError(`device ${id} has an invalid presence state`);
  const label = optionalText(value.user_label, `device ${id} user_label`, maxLabelLength);
  const device: DevicePresence = { id, first_seen: firstSeen, last_seen: lastSeen, state: value.state };
  if (label !== undefined) device.user_label = label;
  return device;
}

function objectValue(input: unknown, field: string): Record<string, unknown> {
  if (typeof input !== "object" || input === null || Array.isArray(input)) throw new DeviceLoadError(`${field} must be an object`);
  return input as Record<string, unknown>;
}

function identifierValue(input: unknown, field: string): string {
  if (typeof input !== "string" || input.length === 0 || input.length > maxIdentifierLength || !identifierPattern.test(input)) {
    throw new DeviceLoadError(`${field} is invalid`);
  }
  return input;
}

function optionalIdentifier(input: unknown, field: string): string | undefined {
  if (input === undefined) return undefined;
  return identifierValue(input, field);
}

function textValue(input: unknown, field: string, maxLength: number): string {
  if (typeof input !== "string" || input.length === 0 || input.length > maxLength || controlCharacters.test(input)) {
    throw new DeviceLoadError(`${field} is invalid`);
  }
  return input;
}

function optionalText(input: unknown, field: string, maxLength: number): string | undefined {
  if (input === undefined) return undefined;
  return textValue(input, field, maxLength);
}

function timestampValue(input: unknown, field: string): string {
  if (typeof input !== "string" || input.length === 0 || input.length > maxTimestampLength || Number.isNaN(Date.parse(input))) {
    throw new DeviceLoadError(`${field} is invalid`);
  }
  return input;
}
