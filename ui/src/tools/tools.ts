const ID_PATTERN = /^[a-z][a-z0-9._:-]{0,127}$/;
const SUPPORT = new Set(["candidate", "planned", "tested", "limited"]);
const OWNERSHIP = new Set(["builtin", "external", "managed-local", "managed-remote"]);
const DESIRED = new Set(["disabled", "enabled"]);
const PROCESS = new Set(["not-applicable", "stopped", "starting", "running", "failed"]);
const VERIFICATION = new Set(["unverified", "verifying", "verified", "degraded", "stale"]);
const REQUIREMENT = new Set(["required", "conditional", "none"]);
const MEASUREMENT = new Set(["unmeasured", "measured"]);
const LIFECYCLE = new Set(["preflight", "install", "connect", "start", "enable", "verify", "disable", "stop", "disconnect", "upgrade", "uninstall"]);

export interface ToolControllerStatus {
  controller_version: string;
  started_at: string;
  config_schema_version: number;
  transport: "unix";
}

export interface ToolCapabilityTarget {
  os: string;
  arch: string;
  min_version?: string;
  support: "candidate" | "planned" | "tested" | "limited";
}

export interface ToolCapabilityPrivilege {
  id: string;
  requirement: "required" | "conditional" | "none";
  description: string;
}

export interface ToolCapabilityResources {
  measurement: "unmeasured" | "measured";
  profile: string;
  max_ram_mib?: number;
  max_disk_mib?: number;
  max_cpu_percent?: number;
  evidence?: string;
}

export interface ToolCapability {
  id: string;
  display_name: string;
  summary: string;
  release: string;
  configured: boolean;
  ownership: "builtin" | "external" | "managed-local" | "managed-remote";
  state: {
    desired: "disabled" | "enabled";
    process: "not-applicable" | "stopped" | "starting" | "running" | "failed";
    verification: "unverified" | "verifying" | "verified" | "degraded" | "stale";
  };
  targets: ToolCapabilityTarget[];
  privileges: ToolCapabilityPrivilege[];
  resources: ToolCapabilityResources;
  provenance: { kind: string; license: string; version_policy: string };
  health: { process_required: boolean; verification_signals: string[]; coverage_requires_verification: boolean };
  lifecycle: string[];
  deep_link_count: number;
}

export interface ToolsSnapshot {
  status: ToolControllerStatus;
  catalog_schema_version: number;
  capabilities: ToolCapability[];
}

export async function loadToolsFromWeb(): Promise<ToolsSnapshot> {
  const [statusResponse, capabilitiesResponse] = await Promise.all([
    fetch("/api/status", { credentials: "same-origin", headers: { Accept: "application/json" } }),
    fetch("/api/capabilities", { credentials: "same-origin", headers: { Accept: "application/json" } }),
  ]);
  if (!statusResponse.ok) throw new Error(await responseMessage(statusResponse, "Controller status is unavailable."));
  if (!capabilitiesResponse.ok) throw new Error(await responseMessage(capabilitiesResponse, "Capability information is unavailable."));
  return parseToolsSnapshot(await statusResponse.json(), await capabilitiesResponse.json());
}

export function parseToolsSnapshot(statusRaw: unknown, capabilitiesRaw: unknown): ToolsSnapshot {
  const status = object(statusRaw, "controller status");
  const capabilities = object(capabilitiesRaw, "capability list");
  const parsedStatus: ToolControllerStatus = {
    controller_version: text(status.controller_version, "controller version", 128),
    started_at: timestamp(status.started_at, "controller started_at"),
    config_schema_version: positiveInt(status.config_schema_version, "config schema version", 1_000_000),
    transport: literal(status.transport, new Set(["unix"]), "controller transport") as "unix",
  };
  const catalogSchemaVersion = positiveInt(capabilities.catalog_schema_version, "catalog schema version", 1_000_000);
  const rawCapabilities = array(capabilities.capabilities, "capabilities", 64);
  const ids = new Set<string>();
  const parsedCapabilities = rawCapabilities.map((raw, index) => parseCapability(raw, index, ids));
  return { status: parsedStatus, catalog_schema_version: catalogSchemaVersion, capabilities: parsedCapabilities };
}

function parseCapability(raw: unknown, index: number, ids: Set<string>): ToolCapability {
  const value = object(raw, `capability ${index}`);
  const id = domainID(value.id, `capability ${index} id`);
  if (ids.has(id)) throw new Error(`duplicate capability id ${id}`);
  ids.add(id);
  const state = object(value.state, `capability ${id} state`);
  const resources = object(value.resources, `capability ${id} resources`);
  const provenance = object(value.provenance, `capability ${id} provenance`);
  const health = object(value.health, `capability ${id} health`);
  const targets = array(value.targets, `capability ${id} targets`, 16).map((target, targetIndex) => parseTarget(target, `${id} target ${targetIndex}`));
  const privileges = array(value.privileges, `capability ${id} privileges`, 32).map((privilege, privilegeIndex) => parsePrivilege(privilege, `${id} privilege ${privilegeIndex}`));
  const verificationSignals = uniqueTextArray(health.verification_signals, `capability ${id} verification signals`, 32, 96);
  const lifecycle = uniqueLiteralArray(value.lifecycle, LIFECYCLE, `capability ${id} lifecycle`, 16);
  const measurement = literal(resources.measurement, MEASUREMENT, `capability ${id} resource measurement`) as "unmeasured" | "measured";
  const parsedResources: ToolCapabilityResources = {
    measurement,
    profile: text(resources.profile, `capability ${id} resource profile`, 96),
  };
  assignOptionalNumber(parsedResources, "max_ram_mib", resources.max_ram_mib, `capability ${id} max RAM`, 1_000_000);
  assignOptionalNumber(parsedResources, "max_disk_mib", resources.max_disk_mib, `capability ${id} max disk`, 100_000_000);
  assignOptionalNumber(parsedResources, "max_cpu_percent", resources.max_cpu_percent, `capability ${id} max CPU`, 100_000);
  const resourceEvidence = optionalText(resources.evidence, `capability ${id} resource evidence`, 512);
  if (resourceEvidence !== undefined) parsedResources.evidence = resourceEvidence;
  if (measurement === "unmeasured" && (parsedResources.max_ram_mib !== undefined || parsedResources.max_disk_mib !== undefined || parsedResources.max_cpu_percent !== undefined)) {
    throw new Error(`capability ${id} unmeasured resources cannot declare measured limits`);
  }
  return {
    id,
    display_name: text(value.display_name, `capability ${id} display name`, 128),
    summary: text(value.summary, `capability ${id} summary`, 1024),
    release: text(value.release, `capability ${id} release`, 64),
    configured: bool(value.configured, `capability ${id} configured`),
    ownership: literal(value.ownership, OWNERSHIP, `capability ${id} ownership`) as ToolCapability["ownership"],
    state: {
      desired: literal(state.desired, DESIRED, `capability ${id} desired state`) as ToolCapability["state"]["desired"],
      process: literal(state.process, PROCESS, `capability ${id} process state`) as ToolCapability["state"]["process"],
      verification: literal(state.verification, VERIFICATION, `capability ${id} verification state`) as ToolCapability["state"]["verification"],
    },
    targets,
    privileges,
    resources: parsedResources,
    provenance: {
      kind: text(provenance.kind, `capability ${id} provenance kind`, 96),
      license: text(provenance.license, `capability ${id} license`, 96),
      version_policy: text(provenance.version_policy, `capability ${id} version policy`, 1024),
    },
    health: {
      process_required: bool(health.process_required, `capability ${id} process required`),
      verification_signals: verificationSignals,
      coverage_requires_verification: bool(health.coverage_requires_verification, `capability ${id} coverage verification`),
    },
    lifecycle,
    deep_link_count: nonNegativeInt(value.deep_link_count, `capability ${id} deep link count`, 32),
  };
}

function parseTarget(raw: unknown, label: string): ToolCapabilityTarget {
  const value = object(raw, label);
  const target: ToolCapabilityTarget = {
    os: text(value.os, `${label} os`, 64),
    arch: text(value.arch, `${label} arch`, 64),
    support: literal(value.support, SUPPORT, `${label} support`) as ToolCapabilityTarget["support"],
  };
  const minVersion = optionalText(value.min_version, `${label} min version`, 64);
  if (minVersion !== undefined) target.min_version = minVersion;
  return target;
}

function parsePrivilege(raw: unknown, label: string): ToolCapabilityPrivilege {
  const value = object(raw, label);
  return {
    id: domainID(value.id, `${label} id`),
    requirement: literal(value.requirement, REQUIREMENT, `${label} requirement`) as ToolCapabilityPrivilege["requirement"],
    description: text(value.description, `${label} description`, 1024),
  };
}

function object(value: unknown, label: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error(`${label} must be an object`);
  return value as Record<string, unknown>;
}

function array(value: unknown, label: string, max: number): unknown[] {
  if (!Array.isArray(value) || value.length > max) throw new Error(`${label} must be an array with at most ${max} items`);
  return value;
}

function text(value: unknown, label: string, max: number): string {
  if (typeof value !== "string" || value.length === 0 || value.length > max || value.trim() !== value) throw new Error(`${label} is invalid`);
  return value;
}

function optionalText(value: unknown, label: string, max: number): string | undefined {
  if (value === undefined) return undefined;
  return text(value, label, max);
}

function domainID(value: unknown, label: string): string {
  const parsed = text(value, label, 128);
  if (!ID_PATTERN.test(parsed)) throw new Error(`${label} is invalid`);
  return parsed;
}

function timestamp(value: unknown, label: string): string {
  const parsed = text(value, label, 64);
  if (!Number.isFinite(Date.parse(parsed))) throw new Error(`${label} is invalid`);
  return parsed;
}

function bool(value: unknown, label: string): boolean {
  if (typeof value !== "boolean") throw new Error(`${label} must be boolean`);
  return value;
}

function literal(value: unknown, allowed: Set<string>, label: string): string {
  if (typeof value !== "string" || !allowed.has(value)) throw new Error(`${label} is unsupported`);
  return value;
}

function positiveInt(value: unknown, label: string, max: number): number {
  if (!Number.isInteger(value) || typeof value !== "number" || value < 1 || value > max) throw new Error(`${label} is invalid`);
  return value;
}

function nonNegativeInt(value: unknown, label: string, max: number): number {
  if (!Number.isInteger(value) || typeof value !== "number" || value < 0 || value > max) throw new Error(`${label} is invalid`);
  return value;
}

function assignOptionalNumber(target: ToolCapabilityResources, key: "max_ram_mib" | "max_disk_mib" | "max_cpu_percent", value: unknown, label: string, max: number): void {
  if (value === undefined) return;
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0 || value > max) throw new Error(`${label} is invalid`);
  target[key] = value;
}

function uniqueTextArray(value: unknown, label: string, maxItems: number, maxLength: number): string[] {
  const items = array(value, label, maxItems).map((item, index) => text(item, `${label} ${index}`, maxLength));
  if (new Set(items).size !== items.length) throw new Error(`${label} contains duplicates`);
  return items;
}

function uniqueLiteralArray(value: unknown, allowed: Set<string>, label: string, maxItems: number): string[] {
  const items = array(value, label, maxItems).map((item, index) => literal(item, allowed, `${label} ${index}`));
  if (new Set(items).size !== items.length) throw new Error(`${label} contains duplicates`);
  return items;
}

async function responseMessage(response: Response, fallback: string): Promise<string> {
  try {
    const value = object(await response.json(), "web error");
    return typeof value.message === "string" && value.message.length <= 512 ? value.message : fallback;
  } catch {
    return fallback;
  }
}
