export type QualityGap = "network-changed" | "permission-required" | "unsupported" | "source-unavailable";
export interface LocalInterfaceCheck {
  source: "os-interface-metadata";
  layer: "local-link";
  method: "interface-state";
  state: "check-succeeded" | "issue-observed" | "not-measured";
  confidence: "limited" | "unknown";
  started_at: string;
  completed_at: string;
  fresh_until: string;
  administrative_up?: boolean;
  gap?: QualityGap;
}
export type LocalQuality =
  | { enrolled: false; as_of: string }
  | { enrolled: true; as_of: string; observer: { interface_name: string; interface_index: number }; check: LocalInterfaceCheck };
export type LocalQualityLoader = (signal: AbortSignal) => Promise<LocalQuality>;

function invalid(): never { throw new Error("The local interface sample is invalid."); }
function object(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return invalid();
  return value as Record<string, unknown>;
}
function fields(value: Record<string, unknown>, required: string[], optional: string[] = []): void {
  if (required.some((key) => !Object.prototype.hasOwnProperty.call(value, key)) ||
      Object.keys(value).some((key) => !required.includes(key) && !optional.includes(key))) invalid();
}
function timestamp(value: unknown): string {
  if (typeof value !== "string" || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value) ||
      !Number.isFinite(Date.parse(value)) || new Date(value).toISOString().slice(0, 19) !== value.slice(0, 19)) return invalid();
  return value;
}
function gapValue(value: unknown): QualityGap {
  if (value === "network-changed" || value === "permission-required" || value === "unsupported" || value === "source-unavailable") return value;
  return invalid();
}

// This parser accepts only the small browser projection, not native DTOs.
export function parseLocalQuality(raw: unknown): LocalQuality {
  const value = object(raw);
  const asOf = timestamp(value.as_of);
  if (value.enrolled === false) {
    fields(value, ["enrolled", "as_of"]);
    return { enrolled: false, as_of: asOf };
  }
  if (value.enrolled !== true) return invalid();
  fields(value, ["enrolled", "as_of", "observer", "check"]);
  const observer = object(value.observer);
  fields(observer, ["interface_name", "interface_index"]);
  if (typeof observer.interface_name !== "string" || !/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$/.test(observer.interface_name) ||
      typeof observer.interface_index !== "number" || !Number.isInteger(observer.interface_index) || observer.interface_index < 1 || observer.interface_index > 2147483647) return invalid();
  const c = object(value.check);
  fields(c, ["source", "layer", "method", "state", "confidence", "started_at", "completed_at", "fresh_until"], ["gap", "administrative_up"]);
  if (c.source !== "os-interface-metadata" || c.layer !== "local-link" || c.method !== "interface-state") return invalid();
  const started = timestamp(c.started_at), completed = timestamp(c.completed_at), fresh = timestamp(c.fresh_until);
  if (completed !== asOf || Date.parse(started) > Date.parse(completed) || Date.parse(completed) - Date.parse(started) > 30_000 ||
      Date.parse(fresh) - Date.parse(completed) !== 30_000) return invalid();
  const state = c.state, confidence = c.confidence;
  if (state !== "check-succeeded" && state !== "issue-observed" && state !== "not-measured") return invalid();
  if (confidence !== "limited" && confidence !== "unknown") return invalid();
  const check: LocalInterfaceCheck = {
    source: c.source, layer: c.layer, method: c.method, state, confidence,
    started_at: started, completed_at: completed, fresh_until: fresh,
  };
  if (state === "not-measured") {
    if (confidence !== "unknown" || Object.prototype.hasOwnProperty.call(c, "administrative_up")) return invalid();
    check.gap = gapValue(c.gap);
  } else {
    if (confidence !== "limited" || Object.prototype.hasOwnProperty.call(c, "gap") || typeof c.administrative_up !== "boolean" ||
        c.administrative_up !== (state === "check-succeeded")) return invalid();
    check.administrative_up = c.administrative_up;
  }
  return { enrolled: true, as_of: asOf, observer: { interface_name: observer.interface_name, interface_index: observer.interface_index }, check };
}

export const loadLocalQualityFromWeb: LocalQualityLoader = async (signal) => {
  const response = await fetch("/api/network-quality", { method: "GET", credentials: "same-origin", cache: "no-store", redirect: "error", signal });
  if (!response.ok || response.headers.get("Content-Type")?.split(";")[0]?.trim().toLowerCase() !== "application/json" || !response.body) {
    await response.body?.cancel();
    throw new Error("The local interface sample is unavailable.");
  }
  // Bound decompressed bytes before JSON parsing, not just the parsed fields.
  const reader = response.body.getReader();
  const decoder = new TextDecoder("utf-8", { fatal: true });
  let bytes = 0, text = "";
  try {
    for (;;) {
      const chunk = await reader.read();
      if (chunk.done) break;
      bytes += chunk.value.byteLength;
      if (bytes > 8192) invalid();
      text += decoder.decode(chunk.value, { stream: true });
    }
    text += decoder.decode();
    return parseLocalQuality(JSON.parse(text) as unknown);
  } finally {
    await reader.cancel().catch(() => undefined);
    reader.releaseLock();
  }
};
