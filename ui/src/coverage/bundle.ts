import { parseCoverageReport } from "./parse";
import type { CoverageReport } from "./types";

const maxCoverageReports = 32;

export interface CoverageBundle {
  as_of: string;
  reports: CoverageReport[];
}

export class CoverageLoadError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "CoverageLoadError";
  }
}

export function parseCoverageBundle(input: unknown): CoverageBundle {
  if (typeof input !== "object" || input === null || Array.isArray(input)) {
    throw new CoverageLoadError("coverage response must be an object");
  }
  const value = input as Record<string, unknown>;
  const asOf = value.as_of;
  if (typeof asOf !== "string" || asOf.length === 0 || asOf.length > 64 || Number.isNaN(Date.parse(asOf))) {
    throw new CoverageLoadError("coverage response has an invalid as_of timestamp");
  }
  if (!Array.isArray(value.reports) || value.reports.length > maxCoverageReports) {
    throw new CoverageLoadError("coverage response has an invalid report collection");
  }
  const reports = value.reports.map((report) => parseCoverageReport(report));
  const seen = new Set<string>();
  for (const report of reports) {
    if (seen.has(report.capability_id)) {
      throw new CoverageLoadError(`duplicate coverage report for ${report.capability_id}`);
    }
    seen.add(report.capability_id);
  }
  return { as_of: asOf, reports };
}

export async function loadCoverageFromWeb(): Promise<CoverageBundle> {
  let response: Response;
  try {
    response = await fetch("/api/coverage", {
      method: "GET",
      headers: { Accept: "application/json" },
      credentials: "same-origin",
      cache: "no-store",
    });
  } catch {
    throw new CoverageLoadError("unable to reach the local Cozy SOC web service");
  }
  if (!response.ok) {
    throw new CoverageLoadError(`live coverage request failed with status ${response.status}`);
  }
  const contentType = response.headers.get("content-type") ?? "";
  if (!contentType.toLowerCase().startsWith("application/json")) {
    throw new CoverageLoadError("live coverage response was not JSON");
  }
  let payload: unknown;
  try {
    payload = await response.json();
  } catch {
    throw new CoverageLoadError("live coverage response was not valid JSON");
  }
  return parseCoverageBundle(payload);
}
