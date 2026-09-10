const maxCandidates = 64;
const maxPrefixes = 64;
const maxInterfaceNameLength = 64;
const maxPrefixLength = 128;
const maxTimestampLength = 64;
const idPattern = /^[a-z][a-z0-9._:-]{0,127}$/;
const csrfPattern = /^[A-Za-z0-9_-]{43}$/;
const controlCharacters = /[\u0000-\u001f\u007f]/;

export interface NetworkInterface {
  interface_name: string;
  interface_index: number;
  prefixes: string[];
}

export interface EnrolledNetwork {
  scope_id: string;
  enrolled_at: string;
  interface: NetworkInterface;
}

export interface NetworkList {
  candidates: NetworkInterface[];
  candidates_truncated: boolean;
  enrolled?: EnrolledNetwork;
}

export interface NetworkEnrollResult extends EnrolledNetwork {
  changed: boolean;
}

export interface DeviceWatchControlResult {
  scope_id?: string;
  changed: boolean;
  active: boolean;
  state: {
    desired: "disabled" | "enabled";
    process: "not-applicable" | "stopped" | "starting" | "running" | "failed";
    verification: "unverified" | "verifying" | "verified" | "degraded" | "stale";
  };
}

export interface SetupClient {
  enrollNetwork(interfaceName: string): Promise<NetworkEnrollResult>;
  enableDeviceWatch(): Promise<DeviceWatchControlResult>;
  disableDeviceWatch(): Promise<DeviceWatchControlResult>;
}

export interface DeviceLabelResult {
  device_id: string;
  user_label: string;
  changed: boolean;
}

export interface DeviceLabelClient {
  labelDevice(deviceID: string, label: string): Promise<DeviceLabelResult>;
}

export class SetupRequestError extends Error {
  readonly code: string;
  readonly status: number;

  constructor(code: string, message: string, status = 0) {
    super(message);
    this.name = "SetupRequestError";
    this.code = code;
    this.status = status;
  }
}

export function parseNetworkList(input: unknown): NetworkList {
  const value = objectValue(input, "network response");
  if (!Array.isArray(value.candidates) || value.candidates.length > maxCandidates) {
    throw new SetupRequestError("invalid_response", "Network response has an invalid candidate collection.");
  }
  if (typeof value.candidates_truncated !== "boolean") {
    throw new SetupRequestError("invalid_response", "Network response has an invalid truncation flag.");
  }
  const candidates = value.candidates.map((item) => parseNetworkInterface(item));
  const seen = new Set<string>();
  for (const candidate of candidates) {
    if (seen.has(candidate.interface_name)) {
      throw new SetupRequestError("invalid_response", `Network response repeats interface ${candidate.interface_name}.`);
    }
    seen.add(candidate.interface_name);
  }

  const result: NetworkList = {
    candidates,
    candidates_truncated: value.candidates_truncated,
  };
  if (value.enrolled !== undefined) result.enrolled = parseEnrolledNetwork(value.enrolled);
  return result;
}

export async function loadNetworksFromWeb(): Promise<NetworkList> {
  const response = await request("/api/networks", { method: "GET" });
  return parseNetworkList(await readJSON(response, "Live network response"));
}

export function createWebSetupClient(): SetupClient & DeviceLabelClient {
  let csrfToken: string | undefined;

  async function csrf(): Promise<string> {
    if (csrfToken !== undefined) return csrfToken;
    const response = await request("/api/session", { method: "GET" });
    const payload = objectValue(await readJSON(response, "Web session response"), "web session response");
    if (typeof payload.csrf_token !== "string" || !csrfPattern.test(payload.csrf_token)) {
      throw new SetupRequestError("invalid_response", "The local web session returned an invalid setup token.");
    }
    csrfToken = payload.csrf_token;
    return csrfToken;
  }

  async function mutate(path: string, body?: unknown): Promise<unknown> {
    const token = await csrf();
    const headers: Record<string, string> = {
      Accept: "application/json",
      "X-Cozy-CSRF": token,
    };
    const init: RequestInit = { method: "POST", headers };
    if (body !== undefined) {
      headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(body);
    }
    const response = await request(path, init);
    return readJSON(response, "Setup response");
  }

  return {
    async enrollNetwork(interfaceName: string) {
      if (!validInterfaceName(interfaceName)) {
        throw new SetupRequestError("invalid_request", "Choose a valid local network interface.");
      }
      return parseNetworkEnrollResult(await mutate("/api/networks/enroll", { interface_name: interfaceName }));
    },
    async enableDeviceWatch() {
      return parseDeviceWatchControl(await mutate("/api/device-watch/enable"));
    },
    async disableDeviceWatch() {
      return parseDeviceWatchControl(await mutate("/api/device-watch/disable"));
    },
    async labelDevice(deviceID: string, label: string) {
      if (!idPattern.test(deviceID)) {
        throw new SetupRequestError("invalid_request", "Device id is invalid. Refresh the device list and try again.");
      }
      const problem = validateDeviceLabelInput(label);
      if (problem !== undefined) throw new SetupRequestError("invalid_request", problem);
      const result = parseDeviceLabelResult(await mutate("/api/devices/label", { device_id: deviceID, label }));
      if (result.device_id !== deviceID || result.user_label !== label) {
        throw new SetupRequestError("invalid_response", "Device label response did not match the requested change.");
      }
      return result;
    },
  };
}

export function validateDeviceLabelInput(label: string): string | undefined {
  if (label !== label.trim()) return "Device labels cannot start or end with whitespace.";
  if (/\p{Cc}/u.test(label)) return "Device labels cannot contain control characters.";
  if (new TextEncoder().encode(label).length > 160) return "Device labels must be 160 UTF-8 bytes or fewer.";
  return undefined;
}

function parseDeviceLabelResult(input: unknown): DeviceLabelResult {
  const value = objectValue(input, "device label response");
  if (typeof value.device_id !== "string" || !idPattern.test(value.device_id)) {
    throw new SetupRequestError("invalid_response", "Device label response has an invalid device id.");
  }
  if (typeof value.changed !== "boolean") {
    throw new SetupRequestError("invalid_response", "Device label response has an invalid changed flag.");
  }
  const userLabel = value.user_label === undefined ? "" : value.user_label;
  if (typeof userLabel !== "string" || validateDeviceLabelInput(userLabel) !== undefined) {
    throw new SetupRequestError("invalid_response", "Device label response has an invalid label.");
  }
  return { device_id: value.device_id, user_label: userLabel, changed: value.changed };
}

function parseNetworkInterface(input: unknown): NetworkInterface {
  const value = objectValue(input, "network interface");
  if (!validInterfaceName(value.interface_name)) {
    throw new SetupRequestError("invalid_response", "Network response has an invalid interface name.");
  }
  if (!Number.isSafeInteger(value.interface_index) || (value.interface_index as number) <= 0) {
    throw new SetupRequestError("invalid_response", "Network response has an invalid interface index.");
  }
  if (!Array.isArray(value.prefixes) || value.prefixes.length === 0 || value.prefixes.length > maxPrefixes) {
    throw new SetupRequestError("invalid_response", "Network response has an invalid prefix collection.");
  }
  const prefixes = value.prefixes.map((prefix) => boundedText(prefix, "network prefix", maxPrefixLength));
  if (new Set(prefixes).size !== prefixes.length) {
    throw new SetupRequestError("invalid_response", "Network response repeats a network prefix.");
  }
  return {
    interface_name: value.interface_name as string,
    interface_index: value.interface_index as number,
    prefixes,
  };
}

function parseEnrolledNetwork(input: unknown): EnrolledNetwork {
  const value = objectValue(input, "enrolled network");
  if (typeof value.scope_id !== "string" || !idPattern.test(value.scope_id)) {
    throw new SetupRequestError("invalid_response", "Enrolled network has an invalid scope id.");
  }
  const enrolledAt = timestampValue(value.enrolled_at, "enrolled_at");
  return {
    scope_id: value.scope_id,
    enrolled_at: enrolledAt,
    interface: parseNetworkInterface(value.interface),
  };
}

function parseNetworkEnrollResult(input: unknown): NetworkEnrollResult {
  const value = objectValue(input, "network enrollment response");
  const enrolled = parseEnrolledNetwork(value);
  if (typeof value.changed !== "boolean") {
    throw new SetupRequestError("invalid_response", "Network enrollment response has an invalid changed flag.");
  }
  return { ...enrolled, changed: value.changed };
}

function parseDeviceWatchControl(input: unknown): DeviceWatchControlResult {
  const value = objectValue(input, "Device Watch response");
  if (typeof value.changed !== "boolean" || typeof value.active !== "boolean") {
    throw new SetupRequestError("invalid_response", "Device Watch response has invalid control flags.");
  }
  const scopeID = value.scope_id;
  if (scopeID !== undefined && (typeof scopeID !== "string" || !idPattern.test(scopeID))) {
    throw new SetupRequestError("invalid_response", "Device Watch response has an invalid scope id.");
  }
  const state = objectValue(value.state, "Device Watch state");
  const desired = enumValue(state.desired, ["disabled", "enabled"] as const, "desired state");
  const process = enumValue(state.process, ["not-applicable", "stopped", "starting", "running", "failed"] as const, "process state");
  const verification = enumValue(state.verification, ["unverified", "verifying", "verified", "degraded", "stale"] as const, "verification state");
  const result: DeviceWatchControlResult = {
    changed: value.changed,
    active: value.active,
    state: { desired, process, verification },
  };
  if (scopeID !== undefined) result.scope_id = scopeID;
  return result;
}

async function request(path: string, init: RequestInit): Promise<Response> {
  let response: Response;
  try {
    const headers = new Headers(init.headers);
    if (!headers.has("Accept")) headers.set("Accept", "application/json");
    response = await fetch(path, {
      ...init,
      headers,
      credentials: "same-origin",
      cache: "no-store",
    });
  } catch {
    throw new SetupRequestError("controller_unavailable", "Unable to reach the local Cozy SOC web service.");
  }
  if (!response.ok) throw await responseError(response);
  return response;
}

async function responseError(response: Response): Promise<SetupRequestError> {
  let code = "request_failed";
  let message = `Local setup request failed with status ${response.status}.`;
  const contentType = response.headers.get("content-type") ?? "";
  if (contentType.toLowerCase().startsWith("application/json")) {
    try {
      const value = objectValue(await response.json(), "error response");
      if (typeof value.error === "string" && value.error.length <= 64 && !controlCharacters.test(value.error)) code = value.error;
      if (typeof value.message === "string" && value.message.length <= 256 && !controlCharacters.test(value.message)) message = value.message;
    } catch {
      // Keep bounded local fallback copy when an error body is malformed.
    }
  }
  return new SetupRequestError(code, message, response.status);
}

async function readJSON(response: Response, label: string): Promise<unknown> {
  const contentType = response.headers.get("content-type") ?? "";
  if (!contentType.toLowerCase().startsWith("application/json")) {
    throw new SetupRequestError("invalid_response", `${label} was not JSON.`);
  }
  try {
    return await response.json();
  } catch {
    throw new SetupRequestError("invalid_response", `${label} was not valid JSON.`);
  }
}

function objectValue(input: unknown, label: string): Record<string, unknown> {
  if (typeof input !== "object" || input === null || Array.isArray(input)) {
    throw new SetupRequestError("invalid_response", `${label} must be an object.`);
  }
  return input as Record<string, unknown>;
}

function boundedText(input: unknown, label: string, maxLength: number): string {
  if (typeof input !== "string" || input.length === 0 || input.length > maxLength || controlCharacters.test(input)) {
    throw new SetupRequestError("invalid_response", `${label} is invalid.`);
  }
  return input;
}

function timestampValue(input: unknown, label: string): string {
  if (typeof input !== "string" || input.length === 0 || input.length > maxTimestampLength || Number.isNaN(Date.parse(input))) {
    throw new SetupRequestError("invalid_response", `${label} is invalid.`);
  }
  return input;
}

function validInterfaceName(input: unknown): input is string {
  return typeof input === "string"
    && input.length > 0
    && input.length <= maxInterfaceNameLength
    && input.trim() === input
    && !controlCharacters.test(input);
}

function enumValue<const T extends readonly string[]>(input: unknown, allowed: T, label: string): T[number] {
  if (typeof input !== "string" || !(allowed as readonly string[]).includes(input)) {
    throw new SetupRequestError("invalid_response", `Device Watch ${label} is invalid.`);
  }
  return input as T[number];
}
