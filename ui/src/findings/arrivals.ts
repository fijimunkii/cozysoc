import { readBoundedWebJSON } from "../web-json";

const idPattern = /^[a-z][a-z0-9._:-]{0,127}$/;

export interface ArrivalFindingItem {
  id: string;
  scope_id: string;
  observed_at: string;
  recorded_at: string;
  evidence_observation_id: string;
  evidence_retained: boolean;
}

export interface ArrivalFindingList {
  as_of: string;
  items: ArrivalFindingItem[];
  truncated: boolean;
}

export function parseArrivalFindings(raw: unknown): ArrivalFindingList {
  const value = object(raw);
  const asOf = timestamp(value.as_of);
  if (typeof value.truncated !== "boolean" || !Array.isArray(value.items) || value.items.length > 100) throw new Error("Arrival finding list is invalid.");
  const seen = new Set<string>();
  const items = value.items.map((rawItem) => {
    const item = object(rawItem);
    const id = identifier(item.id);
    const scopeID = identifier(item.scope_id);
    const observedAt = timestamp(item.observed_at);
    const recordedAt = timestamp(item.recorded_at);
    const evidenceID = identifier(item.evidence_observation_id);
    if (typeof item.evidence_retained !== "boolean" || Date.parse(recordedAt) > Date.parse(asOf) || seen.has(id)) throw new Error("Arrival finding is invalid.");
    seen.add(id);
    return { id, scope_id: scopeID, observed_at: observedAt, recorded_at: recordedAt, evidence_observation_id: evidenceID, evidence_retained: item.evidence_retained };
  });
  for (let i = 1; i < items.length; i++) {
    if (Date.parse(items[i - 1]!.recorded_at) < Date.parse(items[i]!.recorded_at)) throw new Error("Arrival findings are not newest first.");
  }
  return { as_of: asOf, items, truncated: value.truncated };
}

export async function loadArrivalFindings(signal: AbortSignal): Promise<ArrivalFindingList> {
  const response = await fetch("/api/findings/arrivals", { credentials: "same-origin", headers: { Accept: "application/json" }, cache: "no-store", signal });
  if (!response.ok) throw new Error("Arrival findings are unavailable.");
  return parseArrivalFindings(await readBoundedWebJSON(response));
}

function object(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new Error("Arrival findings are invalid.");
  return value as Record<string, unknown>;
}

function identifier(value: unknown): string {
  if (typeof value !== "string" || !idPattern.test(value)) throw new Error("Arrival finding identifier is invalid.");
  return value;
}

function timestamp(value: unknown): string {
  if (typeof value !== "string" || value.length > 64 || !Number.isFinite(Date.parse(value))) throw new Error("Arrival finding time is invalid.");
  return value;
}
