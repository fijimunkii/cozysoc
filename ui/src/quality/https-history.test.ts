import { afterEach, describe, expect, it, vi } from "vitest";
import { loadHTTPSHistoryFromWeb, parseHTTPSHistory, type HTTPSEvidence, type HTTPSHistory } from "./https-history";
import { httpsHistoryFixture as fixture } from "./https-history-fixtures.test-helper";
afterEach(() => vi.restoreAllMocks());
describe("https history boundary", () => {
  it("preserves HTTPS distinctions, optional zero timing, false expectation and owned values", () => {
    for (const evidence of ["unknown", "not-measured", "incomplete", "timeout", "connection-failure", "transport-failure", "tls-failure", "protocol-failure", "status-response", "redirect-response"] satisfies HTTPSEvidence[]) {
      const raw = fixture(evidence), result = parseHTTPSHistory(raw);
      expect(result).toEqual(raw); expect(result.runs[0]?.selection).not.toBe(raw.runs[0]?.selection);
    }
    expect(parseHTTPSHistory(fixture()).runs[0]?.measurement?.response_time_ns).toBe(0);
    expect(parseHTTPSHistory(fixture("redirect-response")).runs[0]?.expectation_matched).toBe(false);
    const expected = fixture("redirect-response"); expected.runs[0]!.selection.expected_status = 301; expected.runs[0]!.expectation_matched = true;
    expect(parseHTTPSHistory(expected)).toEqual(expected);
    const empty = fixture(); empty.enrolled = false; empty.runs = []; expect(parseHTTPSHistory(empty)).toEqual(empty);
    const failed = fixture(); failed.runs[0]!.outcome = "failed"; expect(parseHTTPSHistory(failed)).toEqual(failed);
  });
  const edits: Record<string, (v: HTTPSHistory) => void> = {
    "response before TLS": v => { v.runs[0]!.measurement!.stage = "tls"; },
    "empty stage": v => { v.runs[0]!.measurement!.stage = ""; },
    "invalid expected status": v => { v.runs[0]!.selection.expected_status = 199; },
    "scope absent": v => { v.enrolled = false; }, "window": v => { v.since = v.as_of; },
    "invalid date": v => { v.as_of = "2026-02-30T12:00:00Z"; }, "duplicates": v => { v.runs.push(v.runs[0]!); },
    "oversize": v => { v.runs = Array(21).fill(v.runs[0]); }, "bad reference": v => { v.runs[0]!.selection.id = "<img>"; },
    "bad interface": v => { v.runs[0]!.interface_name = "<script>"; }, "missing index": v => { v.runs[0]!.interface_index = 0; },
    "future": v => { v.runs[0]!.last_audit_at = "2026-09-12T12:00:00.000000001Z"; },
    "no retained phase": v => { const r = v.runs[0]!; r.authorization_retained = r.admission_retained = r.terminal_retained = false; },
    "missing terminal": v => { v.runs[0]!.terminal_retained = false; }, "completed absent": v => { delete v.runs[0]!.measurement; },
    "blocked sample": v => { v.runs[0]!.outcome = "blocked"; }, "missing rtt": v => { delete v.runs[0]!.measurement!.response_time_ns; },
    "big rtt": v => { v.runs[0]!.measurement!.response_time_ns = 8000000000; }, "bad code": v => { v.runs[0]!.measurement!.status_code = 600; },
    "wrong code": v => { v.runs[0]!.measurement!.status_code = 503; }, "wrong result": v => { v.runs[0]!.evidence = "timeout"; },
    "wrong confidence": v => { v.runs[0]!.confidence = "unknown"; }, "wrong expectation": v => { v.runs[0]!.expectation_matched = false; },
    "unaccepted response": v => { v.runs[0]!.measurement!.request = "uncertain"; },
  };
  for (const [name, edit] of Object.entries(edits)) it(`rejects ${name}`, () => { const v = fixture(); edit(v); expect(() => parseHTTPSHistory(v)).toThrow(); });
  it("rejects unknown fields, nulls and malformed optional values", () => {
    const v = fixture();
    for (const raw of [null, { ...v, ticket: "secret" }, { ...v, runs: null }, { ...v, runs: [{ ...v.runs[0], measurement: null }] },
      { ...v, runs: [{ ...v.runs[0], expectation_matched: null }] }]) expect(() => parseHTTPSHistory(raw)).toThrow();
    const timeout = fixture("timeout"); timeout.runs[0]!.measurement!.started_at = timeout.runs[0]!.measurement!.completed_at;
    expect(() => parseHTTPSHistory(timeout)).toThrow();
  });
  it("retains late incomplete cleanup and enforces its duration and phase limits", () => {
    const late = fixture("incomplete"); late.runs[0]!.measurement!.started_at = "2026-09-12T11:59:29Z";
    expect(parseHTTPSHistory(late)).toEqual(late);
    late.runs[0]!.measurement!.started_at = "2026-09-12T11:59:28Z";
    expect(() => parseHTTPSHistory(late)).toThrow();
    const tls = fixture("tls-failure"); tls.runs[0]!.measurement!.request = "accepted";
    expect(() => parseHTTPSHistory(tls)).toThrow();
    const wrongStage = fixture("protocol-failure"); wrongStage.runs[0]!.measurement!.stage = "connect";
    expect(() => parseHTTPSHistory(wrongStage)).toThrow();
  });
  it("makes only a bounded authenticated list read", async () => {
    const fetcher = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify(fixture()), { headers: { "Content-Type": "application/json" } }));
    const signal = new AbortController().signal;
    expect(await loadHTTPSHistoryFromWeb(signal)).toEqual(fixture());
    expect(fetcher).toHaveBeenCalledWith("/api/network-quality/https-history", { method: "GET", credentials: "same-origin", cache: "no-store", redirect: "error", signal });
  });
  it("rejects unavailable, oversized, malformed and wrong-content responses", async () => {
    for (const response of [new Response("private", { status: 503 }), new Response("{}"), new Response("x".repeat(65537), { headers: { "Content-Type": "application/json" } }), new Response(new Uint8Array([255]), { headers: { "Content-Type": "application/json" } })]) {
      vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(response);
      await expect(loadHTTPSHistoryFromWeb(new AbortController().signal)).rejects.toThrow();
    }
  });
});
