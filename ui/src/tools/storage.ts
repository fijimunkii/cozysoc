import { readBoundedWebJSON } from "../web-json";

const QUOTA_STATES = new Set(["current", "pressure", "at-quota"]);
const FILESYSTEM_STATES = new Set(["current", "pressure", "full", "unavailable", "unsupported"]);
const RETENTION_CLASSES = ["ephemeral", "short", "standard", "audit"] as const;

export interface StorageOverview {
  as_of: string;
  quota_state: "current" | "pressure" | "at-quota";
  database_bytes: number;
  used_bytes: number;
  reusable_bytes: number;
  max_bytes: number;
  filesystem_state: string;
  filesystem_supported: boolean;
  filesystem_total_bytes: number;
  filesystem_available_bytes: number;
  retention: { class: typeof RETENTION_CLASSES[number]; duration_seconds: number }[];
}

export async function loadStorageOverviewFromWeb(): Promise<StorageOverview> {
  const response = await fetch("/api/storage", { credentials: "same-origin", headers: { Accept: "application/json" } });
  if (!response.ok) throw new Error("Storage information is unavailable.");
  return parseStorageOverview(await readBoundedWebJSON(response));
}

export function parseStorageOverview(raw: unknown): StorageOverview {
  const value = object(raw);
  const asOf = value.as_of;
  if (typeof asOf !== "string" || !Number.isFinite(Date.parse(asOf))) throw new Error("Storage read time is invalid.");
  if (typeof value.quota_state !== "string" || !QUOTA_STATES.has(value.quota_state)) throw new Error("Storage quota state is invalid.");
  if (typeof value.filesystem_state !== "string" || !FILESYSTEM_STATES.has(value.filesystem_state)) throw new Error("Storage volume state is invalid.");
  if (typeof value.filesystem_supported !== "boolean") throw new Error("Storage volume support is invalid.");
  const databaseBytes = bytes(value.database_bytes);
  const usedBytes = bytes(value.used_bytes);
  const reusableBytes = bytes(value.reusable_bytes);
  const maxBytes = bytes(value.max_bytes);
  const totalBytes = bytes(value.filesystem_total_bytes);
  const availableBytes = bytes(value.filesystem_available_bytes);
  if (maxBytes === 0 || usedBytes + reusableBytes !== databaseBytes || databaseBytes > maxBytes || availableBytes > totalBytes) throw new Error("Storage accounting is invalid.");
  if (!Array.isArray(value.retention) || value.retention.length !== RETENTION_CLASSES.length) throw new Error("Storage retention policy is incomplete.");
  const retention = value.retention.map((rawItem, index) => {
    const item = object(rawItem);
    const retentionClass = RETENTION_CLASSES[index];
    if (retentionClass === undefined || item.class !== retentionClass) throw new Error("Storage retention policy order is invalid.");
    const duration = item.duration_seconds;
    if (typeof duration !== "number" || !Number.isSafeInteger(duration) || duration <= 0 || duration > 10 * 365 * 86400) throw new Error("Storage retention duration is invalid.");
    return { class: retentionClass, duration_seconds: duration };
  });
  return {
    as_of: asOf, quota_state: value.quota_state as StorageOverview["quota_state"],
    database_bytes: databaseBytes, used_bytes: usedBytes, reusable_bytes: reusableBytes, max_bytes: maxBytes,
    filesystem_state: value.filesystem_state, filesystem_supported: value.filesystem_supported,
    filesystem_total_bytes: totalBytes, filesystem_available_bytes: availableBytes, retention,
  };
}

function object(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new Error("Storage overview is invalid.");
  return value as Record<string, unknown>;
}

function bytes(value: unknown): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0) throw new Error("Storage byte count is invalid.");
  return value;
}
