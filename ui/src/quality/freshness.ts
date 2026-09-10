import type { LocalQuality } from "./local-quality";

export interface SampleClock { wall: number; monotonic: number }
export interface QualityReceipt {
  data: LocalQuality;
  received: SampleClock;
  expiresWall: number;
  expiresMonotonic: number;
  invalidClock: boolean;
}
export function sampleClock(): SampleClock { return { wall: Date.now(), monotonic: performance.now() }; }

// Never grant a fresh thirty seconds merely because an old response arrived.
// The absolute evidence deadline and elapsed request time both cap freshness.
export function stampQuality(data: LocalQuality, start: SampleClock, end: SampleClock): QualityReceipt {
  const deadline = data.enrolled ? Date.parse(data.check.fresh_until) : Date.parse(data.as_of);
  const ttl = data.enrolled ? deadline - Date.parse(data.as_of) : 0;
  const elapsed = Math.max(0, end.wall - start.wall, end.monotonic - start.monotonic);
  const remaining = Math.max(0, Math.min(30_000, ttl) - elapsed);
  return {
    data, received: { ...end },
    expiresWall: Math.min(deadline, end.wall + remaining),
    expiresMonotonic: end.monotonic + remaining,
    invalidClock: ![start.wall, start.monotonic, end.wall, end.monotonic].every(Number.isFinite) ||
      end.wall < start.wall || end.monotonic < start.monotonic || Date.parse(data.as_of) > end.wall,
  };
}
export function qualityExpired(receipt: QualityReceipt, now: SampleClock): boolean {
  return receipt.invalidClock || !Number.isFinite(now.wall) || !Number.isFinite(now.monotonic) ||
    now.wall < receipt.received.wall || now.monotonic < receipt.received.monotonic ||
    now.wall >= receipt.expiresWall || now.monotonic >= receipt.expiresMonotonic;
}
