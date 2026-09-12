import { useEffect, useRef, useState } from "react";
import { loadGatewayHistoryFromWeb, type GatewayHistory, type GatewayHistoryLoader, type HistoryEvidence, type HistoryOutcome, type HistoryRun } from "./gateway-history";
import "./local-quality.css";
import "./gateway-history.css";

type View = { kind: "idle" | "loading" | "error" } | { kind: "history"; data: GatewayHistory };
const evidenceCopy: Record<HistoryEvidence, { title: string; explanation: string; next: string }> = {
  "missing-terminal": { title: "Outcome unknown", explanation: "No final record is retained. The check may have been interrupted, still running at the read, or affected by retention.", next: "Read history again later. A missing record does not prove that nothing was sent and does not authorize a retry." },
  "execution-only": { title: "Execution record only", explanation: "This legacy record contains no connectivity measurement.", next: "Do not interpret execution completion as a reply or successful connectivity." },
  "no-measurement": { title: "No usable measurement", explanation: "A final record is retained, but no usable connectivity sample is attached.", next: "Review the recorded execution outcome. Missing evidence cannot establish connectivity or an outage." },
  incomplete: { title: "Incomplete sample", explanation: "Only partial request counts are available. Unsent requests are not timeouts; partial counts do not establish reply loss or latency.", next: "Review the execution outcome before considering a separately authorized check." },
  "all-replied": { title: "All three requests answered", explanation: "The selected target answered all requests during this recorded sample.", next: "Compare evidence from the same time. Replies do not prove current connectivity, gateway identity, or security." },
  "some-replies": { title: "Some requests answered", explanation: "The selected target answered some requests during this recorded sample.", next: "Compare evidence from the same time. This ICMP sample alone cannot diagnose an internet or DNS problem." },
  "no-replies": { title: "No requests answered", explanation: "The selected target did not answer this recorded sample. ICMP filtering or target behavior may explain it.", next: "Check other evidence from the same time before drawing a conclusion. One target's silence does not prove an internet outage." },
};
const outcomeCopy: Record<HistoryOutcome, string> = { unknown: "Unknown", completed: "Completed", blocked: "Blocked before execution", canceled: "Canceled", failed: "Failed", indeterminate: "Indeterminate" };

export function GatewayHistoryPanel({ mode, load = loadGatewayHistoryFromWeb }: { mode: "live" | "demo"; load?: GatewayHistoryLoader }) {
  if (mode === "demo") return <section className="product-card gateway-history" aria-labelledby="gateway-history-title">
    <p className="eyebrow">Network quality · historical evidence</p><h2 id="gateway-history-title">Recent network checks</h2>
    <p>Synthetic demo: retained gateway history is not loaded. Return to live data to read this controller's stored checks.</p>
  </section>;
  return <LiveHistoryPanel load={load} />;
}

function LiveHistoryPanel({ load }: { load: GatewayHistoryLoader }) {
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
  async function readHistory() {
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
  return <section className="product-card gateway-history" aria-labelledby="gateway-history-title">
    <p className="eyebrow">Network quality · historical evidence</p><h2 id="gateway-history-title">Recent network checks</h2>
    <p>Read earlier gateway checks saved by this controller. This sends no probes and does not renew approval to run a check.</p>
    <div aria-busy={view.kind === "loading"}>
      {view.kind === "idle" ? <p>History has not been read yet. There is no background polling.</p> : null}
      {view.kind === "loading" ? <p role="status">Reading retained gateway history…</p> : null}
      {view.kind === "error" ? <p role="alert">Gateway history is unavailable. Retry this read; if the browser session expired, reopen the authenticated local Cozy SOC URL. Other evidence views remain available.</p> : null}
      {view.kind === "history" ? <HistoryDetails data={view.data} /> : null}
    </div>
    <button type="button" className="primary-action" disabled={view.kind === "loading"} onClick={() => { void readHistory(); }}>
      {view.kind === "error" ? "Retry history read" : view.kind === "history" ? "Refresh history list" : "Read gateway history"}
    </button>
    <p className="gateway-history-limits">These are historical ICMP checks of a selected private IPv4 target, whose gateway role is unverified. They do not measure current connectivity, DNS, whole-network quality, security, or monitoring coverage. Creating a check requires the separate experimental native command and one-shot approval.</p>
  </section>;
}
function HistoryDetails({ data }: { data: GatewayHistory }) {
  return <>
    <p role="status">Historical records only · list read at <HistoryTime value={data.as_of} />.</p>
    <p>List window: <HistoryTime value={data.since} /> to <HistoryTime value={data.as_of} />. Up to 20 recent checks are shown. Refreshing reads stored evidence; it never repeats a check.</p>
    {!data.enrolled ? <p>No network was enrolled at this read. History is selected only for the enrolled scope; enrollment and monitoring enablement are separate choices.</p> : data.runs.length === 0 ? <p>No retained gateway checks were found in this bounded window. This does not prove no traffic was sent or that the network is healthy.</p> : null}
    {data.truncated || data.scan_truncated ? <p role="note" className="gateway-history-warning">The history list is incomplete: {data.truncated ? "the 20-run limit was reached. " : ""}{data.scan_truncated ? "the record read limit was reached. " : ""}More retained checks may exist.</p> : null}
    <p>Expired or missing records are not reconstructed. Older retained runs can be inspected with their run reference using the native history command; a reference grants no execution authority.</p>
    {data.runs.length > 0 ? <ul className="gateway-history-list">{data.runs.map((run) => <li key={run.run_id}><RunDetails run={run} /></li>)}</ul> : null}
  </>;
}
function RunDetails({ run }: { run: HistoryRun }) {
  const copy = evidenceCopy[run.evidence], m = run.measurement;
  return <details>
    <summary><strong>{run.target} · {copy.title}</strong><span>{m ? "Sample started" : "Last audit"}: <HistoryTime value={m?.started_at ?? run.last_audit_at} /></span></summary>
    <p>{copy.explanation}</p>
    <dl className="local-quality-facts">
      <div><dt>Historical source</dt><dd>This controller · {run.source} via {run.interface_name} (index {run.interface_index})</dd></div>
      <div><dt>Execution outcome</dt><dd>{outcomeCopy[run.outcome]} · separate from connectivity</dd></div>
      <div><dt>Assessment confidence</dt><dd>{run.confidence === "limited" ? "Limited to this historical target sample" : "Unknown — no complete measurement"}</dd></div>
      <div><dt>Last retained record</dt><dd><HistoryTime value={run.last_audit_at} /></dd></div>
      <div><dt>Retained records</dt><dd>Approval: {run.authorization_retained ? "retained" : "missing"}; run start: {run.admission_retained ? "retained" : "missing"}; result: {run.terminal_retained ? "retained" : "missing"}</dd></div>
      {m ? <>
        <div><dt>Sample completeness</dt><dd>{m.complete ? "Complete" : "Incomplete"}</dd></div>
        <div><dt>Sample started</dt><dd><HistoryTime value={m.started_at} /></dd></div>
        <div><dt>Sample completed</dt><dd>{m.completed_at ? <HistoryTime value={m.completed_at} /> : "Not recorded"}</dd></div>
        <div><dt>Request counts{m.complete ? "" : " (partial)"}</dt><dd>{m.send_calls} send calls · {m.accepted_requests} accepted · {m.replies} replies · {m.timeouts} timeouts</dd></div>
        {m.complete ? <div><dt>ICMP reply loss at sample time</dt><dd>{(100 * m.timeouts / m.accepted_requests).toFixed(1)}% · only this target and request window</dd></div> : null}
        <div><dt>Mean round-trip time</dt><dd>{m.mean_rtt_ns === undefined ? "Unknown" : `${(m.mean_rtt_ns / 1000000).toFixed(3)} ms`}</dd></div>
      </> : null}
      <div><dt>Run reference</dt><dd>{run.run_id}</dd></div>
    </dl>
    <p><strong>Next step:</strong> {copy.next}</p>
    <p>The source and interface describe the original check, not the controller's current network.</p>
  </details>;
}
function HistoryTime({ value }: { value: string }) {
  return <time dateTime={value}>{new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "medium" }).format(new Date(value))}</time>;
}
