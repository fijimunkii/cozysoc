import { afterEach, describe, expect, it, vi } from "vitest";
import { loadQualityDiagnosisFromWeb, parseQualityDiagnosis, type DiagnosisConclusion, type QualityDiagnosis } from "./quality-diagnosis";
import { diagnosisFixture } from "./diagnosis-fixtures.test-helper";
afterEach(() => vi.restoreAllMocks());
describe("historical diagnosis boundary", () => {
  it("preserves every supported interpretation and owns selected references", () => {
    for (const conclusion of ["not-enrolled", "history-incomplete", "insufficient-evidence", "latest-run-unmeasured", "observation-context-mismatch", "observations-too-far-apart", "dns-query-issue-with-responses", "icmp-misses-with-responses", "problems-across-selected-layers", "selected-checks-matched", "mixed-or-limited-evidence"] satisfies DiagnosisConclusion[]) {
      const raw = diagnosisFixture(conclusion), out = parseQualityDiagnosis(raw); expect(out).toEqual(raw);
      if (out.selected[1]?.selection) expect(out.selected[1].selection).not.toBe(raw.selected[1]?.selection);
    }
    const v = diagnosisFixture(); v.selected[1]!.execution_outcome = "failed"; expect(parseQualityDiagnosis(v)).toEqual(v);
  });
  const edits: Record<string, (v: QualityDiagnosis) => void> = {
    window: v => { v.since = v.read_at; }, enrollment: v => { v.enrolled = false; }, "missing anchor": v => { delete v.assessment_at; },
    "read-time anchor": v => { v.assessment_at = v.read_at; }, duplicate: v => { v.selected[1] = v.selected[0]!; },
    "hostile interface": v => { v.selected[0]!.interface_name = "<img>"; }, index: v => { v.selected[0]!.interface_index = 0; },
    "different family": v => { v.selected[1]!.selection!.family = "ipv6"; }, "different interface": v => { v.selected[1]!.interface_index++; },
    "missing sample": v => { delete v.selected[0]!.completed_at; }, "failed complete ICMP": v => { v.selected[0]!.execution_outcome = "failed"; },
    "no terminal": v => { v.selected[1]!.sample_status = "missing-terminal"; }, "missing selection": v => { delete v.selected[1]!.selection; },
    "short ICMP": v => { v.selected[0]!.started_at = v.selected[0]!.completed_at!; },
    "future audit": v => { v.selected[1]!.last_audit_at = "2026-09-12T12:00:00.000000001Z"; },
    "missing evidence bounds": v => { delete v.evidence_start; }, "rewritten evidence time": v => { v.evidence_end = v.read_at; },
    "unsupported references": v => { v.compared[0]!.run_id = "c".repeat(32); }, "duplicate references": v => { v.compared[1] = v.compared[0]!; },
    "truncated confidence": v => { v.truncated = true; }, confidence: v => { v.confidence = "unknown"; },
  };
  for (const [name, edit] of Object.entries(edits)) it(`rejects ${name}`, () => { const v = diagnosisFixture(); edit(v); expect(() => parseQualityDiagnosis(v)).toThrow(); });
  it("rejects null, unrecognized fields, private prose and inflated claims", () => {
    const v = diagnosisFixture();
    for (const raw of [null, { ...v, summary: "private" }, { ...v, assessment_at: null }, { ...v, selected: null }, { ...v, conclusion: "internet-up" }, { ...v, confidence: "high" }, { ...v, selected: [{ ...v.selected[0], selection: null }] }]) expect(() => parseQualityDiagnosis(raw)).toThrow();
  });
  it("keeps the thirty-second boundary unknown", () => {
    const v = diagnosisFixture(); const g = v.selected[0]!; g.started_at = "2026-09-12T11:59:26Z"; g.completed_at = "2026-09-12T11:59:29Z";
    v.conclusion = "insufficient-evidence"; v.confidence = "unknown"; v.compared = []; delete v.evidence_start; delete v.evidence_end;
    expect(parseQualityDiagnosis(v)).toEqual(v);
  });
  it("performs only a bounded authenticated read", async () => {
    const fetcher = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify(diagnosisFixture()), { headers: { "Content-Type": "application/json" } }));
    const signal = new AbortController().signal; expect(await loadQualityDiagnosisFromWeb(signal)).toEqual(diagnosisFixture());
    expect(fetcher).toHaveBeenCalledWith("/api/network-quality/diagnosis", { method: "GET", credentials: "same-origin", cache: "no-store", redirect: "error", signal });
  });
  it("rejects bad status, content type, size and UTF-8", async () => {
    for (const response of [new Response("private", { status: 503 }), new Response("{}"), new Response("x".repeat(65537), { headers: { "Content-Type": "application/json" } }), new Response(new Uint8Array([255]), { headers: { "Content-Type": "application/json" } })]) {
      vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(response); await expect(loadQualityDiagnosisFromWeb(new AbortController().signal)).rejects.toThrow();
    }
  });
});
