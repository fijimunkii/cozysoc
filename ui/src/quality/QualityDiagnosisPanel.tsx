import { useEffect, useRef, useState } from "react";
import { loadQualityDiagnosisFromWeb, type QualityDiagnosis, type QualityDiagnosisLoader, type DiagnosisConclusion, type DiagnosisRun } from "./quality-diagnosis";
import "./local-quality.css";
import "./gateway-history.css";
type View = { kind: "idle" | "loading" | "error" } | { kind: "history"; data: QualityDiagnosis };
const copy: Record<DiagnosisConclusion, { title: string; explanation: string; next: string }> = {
  "not-enrolled": { title: "No network selected", explanation: "No enrolled network was available for this historical read.", next: "Review enrollment. Enrolling a network and enabling monitoring are separate choices; neither approves a check." },
  "history-incomplete": { title: "History is incomplete", explanation: "The bounded history read reached a limit. No comparison is made from this incomplete list.", next: "Inspect individual retained runs. Missing history does not prove that nothing was sent." },
  "insufficient-evidence": { title: "Not enough comparable evidence", explanation: "A gateway and DNS sample from a comparable time window are not both available. Missing or older evidence stays unknown.", next: "Review the selected runs and original times. Any new measurement needs separate native approval." },
  "latest-run-unmeasured": { title: "Latest result cannot be compared", explanation: "At least one latest run has a missing, incomplete or unusable sample. An older success has not been substituted for it.", next: "Inspect the execution outcome and retained history. A missing result does not establish whether traffic was sent or authorize a retry." },
  "observation-context-mismatch": { title: "Checks came from different contexts", explanation: "The selected runs do not share an interface and transport family. IPv4 evidence cannot stand in for IPv6.", next: "Review each check in its original context before comparing results." },
  "observations-too-far-apart": { title: "Checks are too far apart", explanation: "The selected runs fall outside the supported comparison window.", next: "Use the original observation times. Reading again does not make old samples current." },
  "dns-query-issue-with-responses": { title: "DNS query problem alongside other replies", explanation: "The DNS query differed from its recorded expectation or failed, while the selected ICMP check recorded replies nearby in time.", next: "Review the DNS result and original expectation. Resolver policy, filtering or upstream behavior remain possibilities; other replies do not prove the resolver path worked." },
  "icmp-misses-with-responses": { title: "ICMP misses alongside DNS replies", explanation: "The selected ICMP target missed replies while the DNS check received a response nearby in time. Negative and error DNS replies still count as responses.", next: "Review target and protocol behavior, including ICMP filtering, before inferring a wider problem. The target's gateway role is unverified." },
  "problems-across-selected-layers": { title: "Both selected checks recorded problems", explanation: "The ICMP and DNS checks recorded problems nearby in time. They may share dependencies; this does not establish an internet outage or a common cause.", next: "Inspect each original result and the observing interface before collecting any separately approved follow-up." },
  "selected-checks-matched": { title: "Selected checks met their expectations", explanation: "The compared ICMP and DNS samples met their selected expectations during this historical window. An expected negative DNS reply can be a match.", next: "Keep this conclusion limited to these checks and times. It does not prove current connectivity, general internet availability, security or coverage." },
  "mixed-or-limited-evidence": { title: "Evidence remains limited", explanation: "The selected checks can be compared, but their results do not support a wider explanation. A DNS reply may be truncated, a referral or unclassified.", next: "Inspect the individual observations before choosing a separately authorized follow-up." },
};
export function QualityDiagnosisPanel({ mode, load = loadQualityDiagnosisFromWeb }: { mode: "live" | "demo"; load?: QualityDiagnosisLoader }) {
  if (mode === "demo") return <section className="product-card gateway-history" aria-labelledby="quality-diagnosis-title"><p className="eyebrow">Network quality · historical comparison</p><h2 id="quality-diagnosis-title">What earlier checks suggest</h2><p>Synthetic demo: historical diagnosis is not loaded. Return to live data to read this controller's saved evidence.</p></section>;
  return <LiveDiagnosisPanel load={load} />;
}
function LiveDiagnosisPanel({ load }: { load: QualityDiagnosisLoader }) {
  const [view, setView] = useState<View>({ kind: "idle" });
  const request = useRef<AbortController | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    setView({ kind: "idle" });
    return () => {
      request.current?.abort(); request.current = null;
      if (timer.current !== null) clearTimeout(timer.current);
      timer.current = null;
    };
  }, [load]);
  async function readDiagnosis() {
    if (request.current !== null) return;
    const controller = new AbortController(); request.current = controller;
    setView({ kind: "loading" });
    timer.current = setTimeout(() => {
      if (request.current !== controller) return;
      controller.abort(); request.current = null; timer.current = null;
      setView({ kind: "error" });
    }, 6000);
    try {
      const data = await load(controller.signal);
      if (!controller.signal.aborted && request.current === controller) setView({ kind: "history", data });
    } catch {
      if (!controller.signal.aborted && request.current === controller) setView({ kind: "error" });
    } finally {
      if (request.current === controller) {
        request.current = null;
        if (timer.current !== null) clearTimeout(timer.current);
        timer.current = null;
      }
    }
  }
  return <section className="product-card gateway-history" aria-labelledby="quality-diagnosis-title">
    <p className="eyebrow">Network quality · historical comparison</p><h2 id="quality-diagnosis-title">What earlier checks suggest</h2>
    <p>Compare the latest saved gateway and DNS checks. This read sends no probes and grants no approval to repeat a check.</p>
    <div aria-busy={view.kind === "loading"}>
      {view.kind === "idle" ? <p>No comparison has been read yet. There is no background polling.</p> : null}
      {view.kind === "loading" ? <p role="status">Reading historical comparison…</p> : null}
      {view.kind === "error" ? <p role="alert">Historical comparison is unavailable. Retry the read; if the browser session expired, reopen the authenticated local Cozy SOC URL. The individual history views remain available.</p> : null}
      {view.kind === "history" ? <DiagnosisDetails data={view.data} /> : null}
    </div>
    <button type="button" className="primary-action" disabled={view.kind === "loading"} onClick={() => { void readDiagnosis(); }}>{view.kind === "error" ? "Retry comparison read" : view.kind === "history" ? "Refresh comparison" : "Read historical comparison"}</button>
    <p className="gateway-history-limits">This interpretation describes retained evidence, not current connectivity. Nearby times do not prove the same route or a shared cause. Saved runs do not provide a complete sleep, offline or network-change timeline. No local-link or external-target measurements are included.</p>
  </section>;
}
function DiagnosisDetails({ data }: { data: QualityDiagnosis }) {
  const text = copy[data.conclusion];
  return <>
    <h3>{text.title}</h3><p>{text.explanation}</p>
    <p><strong>Next step:</strong> {text.next}</p>
    <dl className="local-quality-facts">
      <div><dt>Confidence</dt><dd>{data.confidence === "limited" ? "Limited to the selected historical checks" : "Unknown — no supported comparison"}</dd></div>
      <div><dt>History read at</dt><dd><HistoryTime value={data.read_at} /></dd></div>
      {data.assessment_at ? <div><dt>Original comparison time</dt><dd><HistoryTime value={data.assessment_at} /></dd></div> : null}
      {data.evidence_start && data.evidence_end ? <div><dt>Compared evidence window</dt><dd><HistoryTime value={data.evidence_start} /> to <HistoryTime value={data.evidence_end} /></dd></div> : null}
    </dl>
    <p>History window: <HistoryTime value={data.since} /> to <HistoryTime value={data.read_at} />. Each layer's history read is limited to 20 runs and 256 audit records. Samples must remain within the fixed 30-second comparison policy at the original comparison time.</p>
    {data.truncated || data.scan_truncated ? <p role="note" className="gateway-history-warning">Comparison withheld: {data.truncated ? "a run limit was reached. " : ""}{data.scan_truncated ? "an audit read limit was reached. " : ""}More retained evidence may exist.</p> : null}
    {data.selected.length ? <ul className="gateway-history-list">{data.selected.map((run) => <li key={`${run.kind}-${run.run_id}`}><RunDetails run={run} compared={data.compared.some((ref) => ref.kind === run.kind && ref.run_id === run.run_id)} /></li>)}</ul> : null}
    <p>Refreshing reads stored evidence. It never replaces the original sample times or repeats a query.</p>
  </>;
}
function RunDetails({ run, compared }: { run: DiagnosisRun; compared: boolean }) {
  const statuses = { "missing-terminal": "Final record missing", "no-measurement": "No usable measurement", incomplete: "Incomplete sample", recorded: "Recorded sample" };
  return <details><summary><strong>{run.kind === "gateway" ? "Selected ICMP check" : "Selected DNS check"} · {statuses[run.sample_status]}</strong><span>{compared ? "Used in this comparison" : "Not compared"} · <HistoryTime value={run.last_audit_at} /></span></summary>
    <dl className="local-quality-facts">
      <div><dt>Historical interface</dt><dd>{run.interface_name} (index {run.interface_index})</dd></div>
      <div><dt>Execution outcome</dt><dd>{run.execution_outcome} · separate from connectivity or DNS result</dd></div>
      {run.started_at ? <div><dt>Sample started</dt><dd><HistoryTime value={run.started_at} /></dd></div> : null}
      {run.completed_at ? <div><dt>Sample ended</dt><dd><HistoryTime value={run.completed_at} /></dd></div> : null}
      {run.selection ? <><div><dt>Recorded DNS expectation</dt><dd>{run.selection.expect === "answer" ? "Answer" : run.selection.expect === "nxdomain" ? "NXDOMAIN" : "NODATA"}</dd></div><div><dt>DNS query profile</dt><dd>{run.selection.family.toUpperCase()} transport · UDP · {run.selection.query_type} question</dd></div><div><dt>Selection reference</dt><dd>{run.selection.id}</dd></div></> : null}
      <div><dt>Run reference</dt><dd>{run.run_id}</dd></div>
    </dl>
    <p>The reference identifies a saved run. It does not approve a retry; missing records do not prove that no traffic was sent.</p>
  </details>;
}
function HistoryTime({ value }: { value: string }) { return <time dateTime={value}>{new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "medium" }).format(new Date(value))}</time>; }
