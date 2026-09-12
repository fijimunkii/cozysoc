import { useEffect, useRef, useState } from "react";
import { loadHTTPSHistoryFromWeb, type HTTPSHistory, type HTTPSHistoryLoader, type HTTPSHistoryRun, type HTTPSEvidence, type HTTPSOutcome } from "./https-history";
import "./local-quality.css";
import "./gateway-history.css";
import "./https-history.css";
type View = { kind: "idle" | "loading" | "error" } | { kind: "history"; data: HTTPSHistory };
const titles: Record<HTTPSEvidence, string> = {
 unknown: "No usable measurement", "not-measured": "Not measured", incomplete: "Incomplete exchange", timeout: "Phase timed out",
 "connection-failure": "Connection failed", "transport-failure": "Transport failed", "tls-failure": "TLS verification or handshake failed",
 "protocol-failure": "HTTP protocol failed", "status-response": "HTTP status received", "redirect-response": "HTTP redirect received",
};
const outcomeCopy: Record<HTTPSOutcome, string> = { unknown: "Unknown", completed: "Completed", blocked: "Blocked before execution", canceled: "Canceled", failed: "Failed", indeterminate: "Indeterminate" };
export function HTTPSHistoryPanel({ mode, load = loadHTTPSHistoryFromWeb }: { mode: "live" | "demo"; load?: HTTPSHistoryLoader }) {
  if (mode === "demo") return <section className="product-card gateway-history https-history" aria-labelledby="https-history-title">
    <p className="eyebrow">Network quality · historical evidence</p><h2 id="https-history-title">Recent HTTPS checks</h2>
    <p>Synthetic demo: retained HTTPS history is not loaded. Return to live data to read this controller's stored checks.</p>
  </section>;
  return <LiveHistoryPanel load={load} />;
}

function LiveHistoryPanel({ load }: { load: HTTPSHistoryLoader }) {
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
  return <section className="product-card gateway-history https-history" aria-labelledby="https-history-title">
    <p className="eyebrow">Network quality · historical evidence</p><h2 id="https-history-title">Recent HTTPS checks</h2>
    <p>Read earlier HTTPS checks saved by this controller. This sends no probes and does not renew approval to run a check.</p>
    <div aria-busy={view.kind === "loading"}>
      {view.kind === "idle" ? <p>History has not been read yet. There is no background polling.</p> : null}
      {view.kind === "loading" ? <p role="status">Reading retained HTTPS history…</p> : null}
      {view.kind === "error" ? <p role="alert">HTTPS history is unavailable. Retry this read; if the browser session expired, reopen the authenticated local Cozy SOC URL. Other evidence views remain available.</p> : null}
      {view.kind === "history" ? <HistoryDetails data={view.data} /> : null}
    </div>
    <button type="button" className="primary-action" disabled={view.kind === "loading"} onClick={() => { void readHistory(); }}>
      {view.kind === "error" ? "Retry HTTPS history read" : view.kind === "history" ? "Refresh HTTPS history list" : "Read HTTPS history"}
    </button>
    <p className="gateway-history-limits">These records describe one selected HTTPS endpoint and request at the original time. HTTP errors and redirects are responses, not packet loss. A single result does not establish current internet availability, a captive portal, security, or monitoring coverage. Creating a check requires the separate experimental native command and one-shot approval.</p>
  </section>;
}
function HistoryDetails({ data }: { data: HTTPSHistory }) {
  return <>
    <p role="status">Historical records only · list read at <HistoryTime value={data.as_of} />.</p>
    <p>List window: <HistoryTime value={data.since} /> to <HistoryTime value={data.as_of} />. Up to 20 recent checks are shown. Refreshing reads stored evidence; it never repeats a check.</p>
    {!data.enrolled ? <p>No network was enrolled at this read. History is selected only for the enrolled scope; enrollment and monitoring enablement are separate choices.</p> : data.runs.length === 0 ? <p>No retained HTTPS checks were found in this bounded window. This does not prove no traffic was sent or that the network is healthy.</p> : null}
    {data.truncated || data.scan_truncated ? <p role="note" className="gateway-history-warning">The history list is incomplete: {data.truncated ? "the 20-run limit was reached. " : ""}{data.scan_truncated ? "the record read limit was reached. " : ""}More retained checks may exist.</p> : null}
    <p>Expired or missing records are not reconstructed. Older retained runs can be inspected with their run reference using the native history command; a reference grants no execution authority.</p>
    {data.runs.length > 0 ? <ul className="gateway-history-list">{data.runs.map((run) => <li key={run.run_id}><RunDetails run={run} /></li>)}</ul> : null}
  </>;
}
function RunDetails({ run }: { run: HTTPSHistoryRun }) {
 const m = run.measurement;
 const title = !run.terminal_retained ? "Outcome unknown" : m?.status_code !== undefined ? `HTTP ${m.status_code} response` : titles[run.evidence];
 return <details>
  <summary><strong>{run.selection.method} · {title}</strong><span>{m ? "Sample started" : "Last audit"}: <HistoryTime value={m?.started_at ?? run.last_audit_at} /></span></summary>
  {!run.terminal_retained ? <p>No final record is retained. Missing records do not prove nothing was sent and do not authorize a retry.</p> : !m ? <p>A final execution record is retained without a usable HTTPS measurement.</p> : null}
  {run.evidence === "timeout" ? <p>The recorded phase reached its deadline. This does not prove packet loss or an internet outage.</p> : null}
  {run.evidence === "tls-failure" ? <p>TLS verification or negotiation failed. This alone does not establish interception or a security finding.</p> : null}
  {run.evidence === "redirect-response" ? <p>This is a 3xx response. The check did not follow redirects or read a response body.</p> : null}
  <dl className="local-quality-facts">
   <div><dt>Observation point</dt><dd>{run.interface_name} (index {run.interface_index}) · {run.selection.family.toUpperCase()}</dd></div>
   <div><dt>Expected HTTP status</dt><dd>{run.selection.expected_status}</dd></div>
   <div><dt>Status expectation</dt><dd>{run.expectation_matched === undefined ? "Unknown" : run.expectation_matched ? "Matched the recorded expectation" : "Differed from the recorded expectation"}</dd></div>
   <div><dt>Execution outcome</dt><dd>{outcomeCopy[run.outcome]} · separate from HTTP status</dd></div>
   <div><dt>Assessment confidence</dt><dd>{run.confidence === "limited" ? "Limited to this historical sample" : "Unknown"}</dd></div>
   <div><dt>Last retained record</dt><dd><HistoryTime value={run.last_audit_at} /></dd></div>
   <div><dt>Retained records</dt><dd>Approval: {run.authorization_retained ? "retained" : "missing"}; run start: {run.admission_retained ? "retained" : "missing"}; result: {run.terminal_retained ? "retained" : "missing"}</dd></div>
   {m ? <>
    <div><dt>Sample started</dt><dd><HistoryTime value={m.started_at} /></dd></div>
    <div><dt>Sample ended</dt><dd><HistoryTime value={m.completed_at} /></dd></div>
    <div><dt>Last phase</dt><dd>{{ "": "Not measured", connect: "Connection", tls: "TLS handshake", request: "HTTP request" }[m.stage]}</dd></div>
    <div><dt>HTTP request submission</dt><dd>{m.request === "accepted" ? "Accepted by the socket" : m.request === "not-sent" ? "HTTP request not sent; connection or TLS traffic may have occurred" : "Uncertain"}</dd></div>
    {m.status_code !== undefined ? <div><dt>Received HTTP status</dt><dd>{m.status_code}</dd></div> : null}
    <div><dt>Time to final response header</dt><dd>{m.response_time_ns === undefined ? "Unknown" : `${(m.response_time_ns / 1000000).toFixed(3)} ms`} · includes connection/TLS work, not pure network RTT</dd></div>
   </> : null}
   <div><dt>Selection reference</dt><dd>{run.selection.id}</dd></div>
   <div><dt>Endpoint reference</dt><dd>{run.selection.endpoint_id}</dd></div>
   <div><dt>Request reference</dt><dd>{run.selection.request_id}</dd></div>
   <div><dt>Run reference</dt><dd>{run.run_id}</dd></div>
  </dl>
  <p>A matching status does not prove body correctness. Compare evidence from the same time; any new check needs separate native approval.</p>
  <p>Private endpoint addresses, TLS names, request paths and response content are not loaded here. The historical interface may differ from the controller's current network.</p>
 </details>;
}
function HistoryTime({ value }: { value: string }) {
  return <time dateTime={value}>{new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "medium" }).format(new Date(value))}</time>;
}
