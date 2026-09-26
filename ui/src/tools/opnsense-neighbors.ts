import { readBoundedWebJSON } from "../web-json";

export interface OPNsenseNeighborReport {
  observation_id: string;
  captured_at: string;
  address: string;
  hardware_address: string;
  interface: string;
  family: "ipv4" | "ipv6";
}

export interface OPNsenseNeighborHistory {
  scope_enrolled: boolean;
  scope_id?: string;
  as_of: string;
  reports: OPNsenseNeighborReport[];
  truncated: boolean;
}

const id = /^[a-z][a-z0-9._:-]{0,127}$/;
const mac = /^([0-9a-f]{2}:){5}[0-9a-f]{2}$/;
const interfaceName = /^[A-Za-z0-9_.:-]{1,32}$/;

export function parseOPNsenseNeighborHistory(input: unknown): OPNsenseNeighborHistory {
  if (!input || typeof input !== "object" || Array.isArray(input)) throw new Error("Invalid router history");
  const value = input as Record<string, unknown>;
  if (typeof value.scope_enrolled !== "boolean" || typeof value.as_of !== "string" || !Number.isFinite(Date.parse(value.as_of)) ||
      typeof value.truncated !== "boolean" || !Array.isArray(value.reports) || value.reports.length > 100) throw new Error("Invalid router history");
  if (!value.scope_enrolled && (value.scope_id !== undefined || value.reports.length !== 0 || value.truncated)) throw new Error("Invalid router scope");
  if (value.scope_enrolled && (typeof value.scope_id !== "string" || !id.test(value.scope_id))) throw new Error("Invalid router scope");
  const reports = value.reports.map((item): OPNsenseNeighborReport => {
    if (!item || typeof item !== "object" || Array.isArray(item)) throw new Error("Invalid router report");
    const report = item as Record<string, unknown>;
    if (typeof report.observation_id !== "string" || !id.test(report.observation_id) ||
        typeof report.captured_at !== "string" || !Number.isFinite(Date.parse(report.captured_at)) ||
        typeof report.address !== "string" || report.address.length > 45 || !/^[0-9a-fA-F:.]+$/.test(report.address) ||
        typeof report.hardware_address !== "string" || !mac.test(report.hardware_address) ||
        typeof report.interface !== "string" || !interfaceName.test(report.interface) ||
        (report.family !== "ipv4" && report.family !== "ipv6")) throw new Error("Invalid router report");
    return { observation_id: report.observation_id, captured_at: report.captured_at,
      address: report.address, hardware_address: report.hardware_address, interface: report.interface, family: report.family };
  });
  return { scope_enrolled: value.scope_enrolled, ...(value.scope_enrolled ? { scope_id: value.scope_id as string } : {}),
    as_of: value.as_of, reports, truncated: value.truncated };
}

export async function loadOPNsenseNeighborHistory(signal: AbortSignal): Promise<OPNsenseNeighborHistory> {
  const response = await fetch("/api/opnsense/neighbors", { method: "GET", headers: { Accept: "application/json" }, credentials: "same-origin", cache: "no-store", signal });
  if (!response.ok) throw new Error("Router history is unavailable");
  return parseOPNsenseNeighborHistory(await readBoundedWebJSON(response));
}
