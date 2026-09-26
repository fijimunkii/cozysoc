const COVERAGE_STATES = new Set(["unconfigured", "unavailable", "unverified", "active-limited", "degraded", "stale", "disconnected", "unknown"]);
const FAILURE_CATEGORIES = new Set(["none", "not-configured", "read-failed", "sensor", "ingestion", "storage", "evidence", "source", "unknown"]);
const VERIFICATION_STATES = new Set(["unverified", "verifying", "verified", "degraded", "stale", "unknown"]);
const DESIRED_STATES = new Set(["enabled", "disabled", "unknown"]);

export interface DiagnosticPreview {
  schema_version: 1;
  generated_at: string;
  controller: { build_version: string; config_schema_version: number; health_state: "ok" | "degraded" | "unknown"; gap_count: number };
  modules: { id: "device-watch"; build_version: string; desired: string; verification: string }[];
  coverage: { capability_id: "device-watch"; state: string; failure_category: string }[];
}

export async function loadDiagnosticPreview(signal: AbortSignal): Promise<DiagnosticPreview> {
  const response = await fetch("/api/diagnostics/preview", { credentials: "same-origin", headers: { Accept: "application/json" }, signal });
  if (!response.ok) throw new Error("Diagnostic preview is unavailable.");
  return parseDiagnosticPreview(await response.json());
}

export function parseDiagnosticPreview(raw: unknown): DiagnosticPreview {
  const value = object(raw);
  if (value.schema_version !== 1) throw new Error("Unsupported diagnostic version.");
  const generatedAt = value.generated_at;
  if (typeof generatedAt !== "string" || !Number.isFinite(Date.parse(generatedAt))) throw new Error("Diagnostic time is invalid.");
  const controller = object(value.controller);
  const buildVersion = controller.build_version;
  if (typeof buildVersion !== "string" || !/^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$/.test(buildVersion)) throw new Error("Diagnostic build version is invalid.");
  const configSchema = boundedInt(controller.config_schema_version, 1, 1_000_000);
  const gapCount = boundedInt(controller.gap_count, 0, Number.MAX_SAFE_INTEGER);
  const healthState = allowed(controller.health_state, new Set(["ok", "degraded", "unknown"]));
  const rawModules = value.modules;
  if (!Array.isArray(rawModules) || rawModules.length > 1) throw new Error("Diagnostic module list is invalid.");
  const modules = rawModules.map((rawModule) => {
    const module = object(rawModule);
    if (module.id !== "device-watch" || module.build_version !== buildVersion) throw new Error("Diagnostic module is invalid.");
    return { id: "device-watch" as const, build_version: buildVersion, desired: allowed(module.desired, DESIRED_STATES), verification: allowed(module.verification, VERIFICATION_STATES) };
  });
  const rawCoverage = value.coverage;
  if (!Array.isArray(rawCoverage) || rawCoverage.length !== 1) throw new Error("Diagnostic coverage is invalid.");
  const coverage = rawCoverage.map((rawItem) => {
    const item = object(rawItem);
    if (item.capability_id !== "device-watch") throw new Error("Diagnostic capability is invalid.");
    return { capability_id: "device-watch" as const, state: allowed(item.state, COVERAGE_STATES), failure_category: allowed(item.failure_category, FAILURE_CATEGORIES) };
  });
  return { schema_version: 1, generated_at: generatedAt, controller: { build_version: buildVersion, config_schema_version: configSchema, health_state: healthState as DiagnosticPreview["controller"]["health_state"], gap_count: gapCount }, modules, coverage };
}

function object(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new Error("Diagnostic preview is invalid.");
  return value as Record<string, unknown>;
}

function allowed(value: unknown, values: Set<string>): string {
  if (typeof value !== "string" || !values.has(value)) throw new Error("Diagnostic state is invalid.");
  return value;
}

function boundedInt(value: unknown, min: number, max: number): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < min || value > max) throw new Error("Diagnostic count is invalid.");
  return value;
}
