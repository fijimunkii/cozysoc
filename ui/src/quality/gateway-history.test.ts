import { afterEach, describe, expect, it, vi } from "vitest";
import { loadGatewayHistoryFromWeb, parseGatewayHistory, type GatewayHistory, type HistoryEvidence } from "./gateway-history";
import { historyFixture } from "./history-fixtures.test-helper";

afterEach(() => vi.unstubAllGlobals());
describe("gateway history browser contract", () => {
  it("preserves historical evidence states and measured zero", () => {
    for (const evidence of ["missing-terminal", "execution-only", "no-measurement", "incomplete", "all-replied", "some-replies", "no-replies"] satisfies HistoryEvidence[]) {
      const raw = historyFixture(evidence);
      expect(parseGatewayHistory(raw)).toEqual(raw);
    }
    expect(parseGatewayHistory(historyFixture()).runs[0]?.measurement?.mean_rtt_ns).toBe(0);
    const empty = historyFixture(); empty.enrolled = false; empty.runs = [];
    expect(parseGatewayHistory(empty)).toEqual(empty);
  });
  const edits: Record<string, (v: GatewayHistory) => void> = {
    "unenrolled records": (v) => { v.enrolled = false; },
    "unbounded runs": (v) => { v.runs = Array.from({ length: 21 }, () => v.runs[0]!); },
    "duplicate ids": (v) => { v.runs.push(v.runs[0]!); },
    "invalid date": (v) => { v.as_of = "2026-02-30T12:00:00Z"; },
    "window widening": (v) => { v.since = "2026-09-10T12:00:00Z"; },
    "outside nanosecond storage range": (v) => { v.as_of = "9999-01-01T00:00:00Z"; },
    "future audit": (v) => { v.runs[0]!.last_audit_at = "2026-09-12T12:00:00.000000001Z"; },
    "outside window": (v) => { v.runs[0]!.last_audit_at = "2026-09-11T11:59:59Z"; },
    "hostile interface": (v) => { v.runs[0]!.interface_name = "<script>"; },
    "fractional index": (v) => { v.runs[0]!.interface_index = 1.5; },
    "public target": (v) => { v.runs[0]!.target = "8.8.8.8"; },
    "IPv4 overflow": (v) => { v.runs[0]!.target = "192.168.50.256"; },
    "ambiguous address": (v) => { v.runs[0]!.target = "192.168.050.1"; },
    "same source": (v) => { v.runs[0]!.source = v.runs[0]!.target; },
    "missing terminal with sample": (v) => { v.runs[0]!.terminal_retained = false; },
    "legacy with sample": (v) => { v.runs[0]!.evidence = "execution-only"; },
    "absent completed sample": (v) => { delete v.runs[0]!.measurement; },
    "too many sends": (v) => { v.runs[0]!.measurement!.send_calls = 4; },
    "unaccepted replies": (v) => { v.runs[0]!.measurement!.accepted_requests = 2; },
    "invented timeouts": (v) => { v.runs[0]!.measurement!.timeouts = 1; },
    "missing RTT": (v) => { delete v.runs[0]!.measurement!.mean_rtt_ns; },
    "unbounded RTT": (v) => { v.runs[0]!.measurement!.mean_rtt_ns = 1000000000; },
    "incomplete latency": (v) => { v.runs[0]!.measurement!.complete = false; },
    "too fast pacing": (v) => { v.runs[0]!.measurement!.started_at = "2026-09-12T11:59:58Z"; },
    "too long sample": (v) => { v.runs[0]!.measurement!.started_at = "2026-09-12T11:59:54Z"; },
    "blocked measurement": (v) => { v.runs[0]!.outcome = "blocked"; },
    "failed complete sample": (v) => { v.runs[0]!.outcome = "failed"; },
    "contradictory assessment": (v) => { v.runs[0]!.evidence = "no-replies"; },
    "wrong confidence": (v) => { v.runs[0]!.confidence = "unknown"; },
  };
  for (const [name, edit] of Object.entries(edits)) it(`rejects ${name}`, () => {
    const value = historyFixture(); edit(value); expect(() => parseGatewayHistory(value)).toThrow();
  });
  it("rejects unknown fields, malformed shapes and null optional metrics", () => {
    const v = historyFixture();
    for (const raw of [null, [], {}, { ...v, runs: null }, { ...v, scope_id: "private" }, { ...v, runs: [{ ...v.runs[0], challenge: "private" }] },
      { ...v, runs: [{ ...v.runs[0], measurement: null }] }, { ...v, runs: [{ ...v.runs[0], outcome: "internet-down" }] },
      { ...v, runs: [{ ...v.runs[0], confidence: "high" }] }]) expect(() => parseGatewayHistory(raw)).toThrow();
  });
  it("preserves sub-millisecond bounds without rejecting a valid near-deadline sample", () => {
    const v = historyFixture(); v.runs[0]!.measurement!.started_at = "2026-09-12T11:59:54.000000001Z";
    expect(parseGatewayHistory(v)).toEqual(v);
  });
  it("uses only the fixed authenticated read, with cancellation, no cache and no redirects", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify(historyFixture()), { headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);
    const controller = new AbortController();
    expect(await loadGatewayHistoryFromWeb(controller.signal)).toEqual(historyFixture());
    expect(fetcher).toHaveBeenCalledWith("/api/network-quality/history", { method: "GET", credentials: "same-origin", cache: "no-store", redirect: "error", signal: controller.signal });
  });
  it("rejects oversized, malformed, non-JSON, unavailable and invalid UTF-8 responses", async () => {
    for (const response of [new Response("private", { status: 503 }), new Response("private", { status: 401 }),
      new Response("{}", { headers: { "Content-Type": "text/html" } }), new Response(" ".repeat(65537), { headers: { "Content-Type": "application/json" } }),
      new Response("{", { headers: { "Content-Type": "application/json" } }), new Response(new Uint8Array([255]), { headers: { "Content-Type": "application/json" } })]) {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response));
      await expect(loadGatewayHistoryFromWeb(new AbortController().signal)).rejects.toThrow();
    }
  });
});
