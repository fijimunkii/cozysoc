import { readBoundedWebJSON } from "../web-json";
import { parseAdminOrigin } from "./adguard-status";

export interface OPNsenseStatus {
  connected: boolean;
  endpoint?: string;
  version?: string;
}

export function parseOPNsenseStatus(input: unknown): OPNsenseStatus {
  if (!input || typeof input !== "object" || Array.isArray(input)) throw new Error("Invalid OPNsense status");
  const value = input as Record<string, unknown>;
  if (value.connected === false) return { connected: false };
  if (value.connected !== true || value.version !== "26.7.4") throw new Error("Invalid OPNsense status");
  const endpoint = parseAdminOrigin(value.endpoint);
  if (!endpoint.startsWith("https://")) throw new Error("Invalid OPNsense origin");
  return { connected: true, endpoint, version: value.version };
}

export async function loadOPNsenseStatus(signal: AbortSignal): Promise<OPNsenseStatus> {
  const response = await fetch("/api/opnsense/status", { method: "GET", headers: { Accept: "application/json" }, credentials: "same-origin", cache: "no-store", signal });
  if (!response.ok) throw new Error("OPNsense status is unavailable");
  return parseOPNsenseStatus(await readBoundedWebJSON(response));
}
