import { httpsHistoryFixture } from "./https-history-fixtures.test-helper";
import type { DiagnosisConclusion, QualityDiagnosis } from "./quality-diagnosis";
export function diagnosisFixture(conclusion: DiagnosisConclusion = "selected-checks-matched"): QualityDiagnosis {
  const out: QualityDiagnosis = { enrolled: true, read_at: "2026-09-12T12:00:00Z", since: "2026-09-11T12:00:00Z", truncated: false, scan_truncated: false,
    assessment_at: "2026-09-12T11:59:59Z", evidence_start: "2026-09-12T11:59:55Z", evidence_end: "2026-09-12T11:59:59Z", conclusion, confidence: "limited",
    selected: [{ kind: "gateway", run_id: "a".repeat(32), interface_name: "en0", interface_index: 7, last_audit_at: "2026-09-12T11:59:58Z", execution_outcome: "completed", sample_status: "recorded", started_at: "2026-09-12T11:59:55Z", completed_at: "2026-09-12T11:59:58Z" },
      { kind: "resolver", run_id: "b".repeat(32), interface_name: "en0", interface_index: 7, last_audit_at: "2026-09-12T11:59:59Z", execution_outcome: "completed", sample_status: "recorded", started_at: "2026-09-12T11:59:58Z", completed_at: "2026-09-12T11:59:59Z", selection: { id: "selection.test", resolver_id: "resolver.test", query_id: "query.test", family: "ipv4", transport: "udp", query_type: "AAAA", expect: "nxdomain" } }],
    compared: [{ kind: "gateway", run_id: "a".repeat(32) }, { kind: "resolver", run_id: "b".repeat(32) }] };
  if (["not-enrolled", "history-incomplete", "insufficient-evidence", "latest-run-unmeasured", "observation-context-mismatch", "observations-too-far-apart"].includes(conclusion)) {
    out.confidence = "unknown"; out.compared = []; delete out.evidence_start; delete out.evidence_end;
    switch (conclusion) {
      case "not-enrolled": out.enrolled = false; out.selected = []; delete out.assessment_at; break;
      case "insufficient-evidence": out.selected = []; delete out.assessment_at; break;
      case "history-incomplete": out.scan_truncated = true; break;
      case "latest-run-unmeasured": { const g = out.selected[0]!; g.sample_status = "missing-terminal"; g.execution_outcome = "unknown"; delete g.started_at; delete g.completed_at; break; }
      case "observation-context-mismatch": out.selected[1]!.selection!.family = "ipv6"; break;
      case "observations-too-far-apart": out.selected[0]!.started_at = "2026-09-11T11:59:55Z"; out.selected[0]!.completed_at = "2026-09-11T11:59:58Z"; break;
    }
  }
  return out;
}

export function httpsDiagnosisFixture(): QualityDiagnosis {
 const out = diagnosisFixture();
 const h = httpsHistoryFixture().runs[0]!;
 h.run_id = "c".repeat(32); h.selection.expected_status = 503; h.measurement!.status_code = 503;
 out.selected.push({ kind: "https", run_id: h.run_id, interface_name: h.interface_name, interface_index: h.interface_index, last_audit_at: h.last_audit_at, execution_outcome: h.outcome, sample_status: "recorded", started_at: h.measurement!.started_at, completed_at: h.measurement!.completed_at, https: h });
 out.compared.push({kind: "https", run_id: h.run_id});
 return out;
}
