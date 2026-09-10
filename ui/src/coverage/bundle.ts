import { parseCoverageReport } from "./parse";
import type { CoverageReport } from "./types";

const maxCoverageReports = 32;
const bootstrapPattern = /^[A-Za-z0-9_-]{43}$/;

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
  await establishWebSessionFromFragment();

  let response: Response;
  try {
    response = await fetch("/api/coverage", {
      method: "GET",
      headers: { Accept: "application/json" },
      credentials: "same-origin",
      cache: "no-store",
    });
  } catch {
    throw new CoverageLoadError("Unable to reach the local Cozy SOC web service.");
  }
  if (response.status === 401) {
    throw new CoverageLoadError("Open the authenticated local URL printed by `cozysoc web`, then retry.");
  }
  if (!response.ok) {
    throw new CoverageLoadError(`Live coverage request failed with status ${response.status}.`);
  }
  const contentType = response.headers.get("content-type") ?? "";
  if (!contentType.toLowerCase().startsWith("application/json")) {
    throw new CoverageLoadError("Live coverage response was not JSON.");
  }
  let payload: unknown;
  try {
    payload = await response.json();
  } catch {
    throw new CoverageLoadError("Live coverage response was not valid JSON.");
  }
  return parseCoverageBundle(payload);
}

async function establishWebSessionFromFragment(): Promise<void> {
  const hash = window.location.hash.startsWith("#") ? window.location.hash.slice(1) : window.location.hash;
  if (hash === "") return;

  const params = new URLSearchParams(hash);
  const bootstrap = params.get("bootstrap");
  if (bootstrap === null) return;

  try {
    if (!bootstrapPattern.test(bootstrap)) {
      throw new CoverageLoadError("The local web bootstrap value is malformed. Restart `cozysoc web` and use its new URL.");
    }
    let response: Response;
    try {
      response = await fetch("/api/session", {
        method: "POST",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/json",
        },
        credentials: "same-origin",
        cache: "no-store",
        body: JSON.stringify({ bootstrap }),
      });
    } catch {
      throw new CoverageLoadError("Unable to establish the local Cozy SOC web session.");
    }
    if (!response.ok) {
      throw new CoverageLoadError("The local Cozy SOC web session could not be established. Restart `cozysoc web` and use its new URL.");
    }
  } finally {
    window.history.replaceState(null, document.title, window.location.pathname + window.location.search);
  }
}
