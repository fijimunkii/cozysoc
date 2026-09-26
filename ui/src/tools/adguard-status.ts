import { readBoundedWebJSON } from "../web-json";

export interface AdGuardStatus {
  connected: boolean;
  endpoint?: string;
  version?: string;
  running: boolean;
  protection_enabled: boolean;
  filtering_enabled: boolean;
  query_log_enabled: boolean;
  anonymized_clients: boolean;
}

export function parseAdGuardStatus(input: unknown): AdGuardStatus {
  if (typeof input !== "object" || input === null || Array.isArray(input)) throw new Error("Invalid AdGuard Home status");
  const value = input as Record<string, unknown>;
  if (typeof value.connected !== "boolean") throw new Error("Invalid AdGuard Home connection state");
  if (!value.connected) return { connected: false, running: false, protection_enabled: false, filtering_enabled: false, query_log_enabled: false, anonymized_clients: false };
  const endpoint = parseAdminOrigin(value.endpoint);
  const fields = ["running", "protection_enabled", "filtering_enabled", "query_log_enabled", "anonymized_clients"] as const;
  for (const field of fields) if (typeof value[field] !== "boolean") throw new Error("Invalid AdGuard Home service state");
  if (typeof value.version !== "string" || value.version.length > 64 || /[\u0000-\u001f\u007f]/.test(value.version)) throw new Error("Invalid AdGuard Home version");
  return { connected: true, endpoint, version: value.version, running: value.running as boolean, protection_enabled: value.protection_enabled as boolean, filtering_enabled: value.filtering_enabled as boolean, query_log_enabled: value.query_log_enabled as boolean, anonymized_clients: value.anonymized_clients as boolean };
}

function parseAdminOrigin(input: unknown): string {
  if (typeof input !== "string" || input.length > 128) throw new Error("Invalid AdGuard Home admin origin");
  let url: URL;
  try { url = new URL(input); } catch { throw new Error("Invalid AdGuard Home admin origin"); }
  if ((url.protocol !== "https:" && url.protocol !== "http:") || url.username || url.password || url.search || url.hash || url.pathname !== "/") throw new Error("Invalid AdGuard Home admin origin");
  const host = url.hostname;
  const ipv6 = host.startsWith("[") && host.endsWith("]") && host.includes(":");
  const octets = host.split(".");
  const ipv4 = octets.length === 4 && octets.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255);
  if (!ipv4 && !ipv6) throw new Error("AdGuard Home admin origin must use a literal IP address");
  if (url.protocol === "http:" && !(ipv4 && Number(octets[0]) === 127) && host !== "[::1]") throw new Error("Remote AdGuard Home admin origin requires HTTPS");
  return url.origin;
}

export async function loadAdGuardStatus(signal: AbortSignal): Promise<AdGuardStatus> {
  const response = await fetch("/api/adguard/status", { method: "GET", headers: { Accept: "application/json" }, credentials: "same-origin", cache: "no-store", signal });
  if (!response.ok) throw new Error("AdGuard Home status is unavailable");
  return parseAdGuardStatus(await readBoundedWebJSON(response));
}

// Browser cookies are host-scoped, not port-scoped. A new-tab navigation to
// another service on the web UI's host would carry its HttpOnly session cookie.
export function adminLinkSharesWebCookieHost(endpoint: string, webHostname: string): boolean {
  return new URL(endpoint).hostname === webHostname;
}
