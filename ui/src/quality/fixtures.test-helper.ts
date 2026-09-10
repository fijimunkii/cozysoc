export const qualityAsOf = "2026-09-10T12:00:00.000Z";
// Synthetic protocol values, never a real enrolled network or OS read.
export function qualityRaw(): Record<string, unknown> & { observer: Record<string, unknown>; check: Record<string, unknown> } {
  return {
    enrolled: true, as_of: qualityAsOf,
    observer: { interface_name: "en0", interface_index: 7 },
    check: {
      source: "os-interface-metadata", layer: "local-link", method: "interface-state",
      state: "check-succeeded", confidence: "limited", administrative_up: true,
      started_at: qualityAsOf, completed_at: qualityAsOf, fresh_until: "2026-09-10T12:00:30.000Z",
    },
  };
}
