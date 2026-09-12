import { useEffect, useRef, useState } from "react";
import { loadResolverHistoryFromWeb, type ResolverHistory, type ResolverHistoryLoader, type ResolverHistoryRun, type ResolverEvidence, type ResolverOutcome } from "./resolver-history";
import "./local-quality.css";
import "./gateway-history.css";
type View = { kind: "idle" | "loading" | "error" } | { kind: "history"; data: ResolverHistory };
const titles: Record<ResolverEvidence, string> = {
  unknown: "No usable measurement", "not-measured": "Not measured", incomplete: "Incomplete exchange", timeout: "No matched reply before timeout",
  "transport-failure": "Transport failure", answer: "Answer returned", nxdomain: "Name does not exist (NXDOMAIN)", "no-data": "No data of this type (NODATA)",
  refused: "Query refused (REFUSED)", "server-failure": "Resolver reported failure (SERVFAIL)", "format-error": "Query format error (FORMERR)",
  "not-implemented": "Operation unsupported (NOTIMP)", "other-response-error": "Other DNS error reply", referral: "Referral returned",
  truncated: "Truncated reply", "unclassified-response": "Unclassified reply",
};
const outcomeCopy: Record<ResolverOutcome, string> = { unknown: "Unknown", completed: "Completed", blocked: "Blocked before execution", canceled: "Canceled", failed: "Failed", indeterminate: "Indeterminate" };
export function ResolverHistoryPanel({ mode, load = loadResolverHistoryFromWeb }: { mode: "live" | "demo"; load?: ResolverHistoryLoader }) {
  if (mode === "demo") return <section className="product-card gateway-history" aria-labelledby="resolver-history-title">
    <p className="eyebrow">Network quality · historical evidence</p><h2 id="resolver-history-title">Recent DNS checks</h2>
    <p>Synthetic demo: retained resolver history is not loaded. Return to live data to read this controller's stored checks.</p>
  </section>;
  return <LiveHistoryPanel load={load} />;
}

function LiveHistoryPanel({ load }: { load: ResolverHistoryLoader }) {
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
  return <section className="product-card gateway-history" aria-labelledby="resolver-history-title">
    <p className="eyebrow">Network quality · historical evidence</p><h2 id="resolver-history-title">Recent DNS checks</h2>
    <p>Read earlier DNS checks saved by this controller. This sends no probes and does not renew approval to run a check.</p>
    <div aria-busy={view.kind === "loading"}>
      {view.kind === "idle" ? <p>History has not been read yet. There is no background polling.</p> : null}
      {view.kind === "loading" ? <p role="status">Reading retained resolver history…</p> : null}
      {view.kind === "error" ? <p role="alert">Resolver history is unavailable. Retry this read; if the browser session expired, reopen the authenticated local Cozy SOC URL. Other evidence views remain available.</p> : null}
      {view.kind === "history" ? <HistoryDetails data={view.data} /> : null}
    </div>
    <button type="button" className="primary-action" disabled={view.kind === "loading"} onClick={() => { void readHistory(); }}>
      {view.kind === "error" ? "Retry DNS history read" : view.kind === "history" ? "Refresh DNS history list" : "Read DNS history"}
    </button>
    <p className="gateway-history-limits">These records describe one selected resolver and question at the original sample time. A DNS error reply is a response, not packet loss. A timeout or one resolver failure does not prove an internet outage, current DNS health, security, or monitoring coverage. Creating a check requires the separate experimental native command and one-shot approval.</p>
  </section>;
}
function HistoryDetails({ data }: { data: ResolverHistory }) {
  return <>
    <p role="status">Historical records only · list read at <HistoryTime value={data.as_of} />.</p>
    <p>List window: <HistoryTime value={data.since} /> to <HistoryTime value={data.as_of} />. Up to 20 recent checks are shown. Refreshing reads stored evidence; it never repeats a check.</p>
    {!data.enrolled ? <p>No network was enrolled at this read. History is selected only for the enrolled scope; enrollment and monitoring enablement are separate choices.</p> : data.runs.length === 0 ? <p>No retained DNS checks were found in this bounded window. This does not prove no traffic was sent or that the network is healthy.</p> : null}
    {data.truncated || data.scan_truncated ? <p role="note" className="gateway-history-warning">The history list is incomplete: {data.truncated ? "the 20-run limit was reached. " : ""}{data.scan_truncated ? "the record read limit was reached. " : ""}More retained checks may exist.</p> : null}
    <p>Expired or missing records are not reconstructed. Older retained runs can be inspected with their run reference using the native history command; a reference grants no execution authority.</p>
    {data.runs.length > 0 ? <ul className="gateway-history-list">{data.runs.map((run) => <li key={run.run_id}><RunDetails run={run} /></li>)}</ul> : null}
  </>;
}
function RunDetails({ run }: { run: ResolverHistoryRun }) {
  const m = run.measurement, title = run.terminal_retained ? titles[run.evidence] : "Outcome unknown";
  return <details>
    <summary><strong>{run.selection.query_type} · {title}</strong><span>{m ? "Sample started" : "Last audit"}: <HistoryTime value={m?.started_at ?? run.last_audit_at} /></span></summary>
    {!run.terminal_retained ? <p>No final record is retained. This may reflect interruption, an in-flight check, or retention. Missing records do not prove nothing was sent and do not authorize a retry.</p> : !m ? <p>A final execution record is retained without a usable DNS measurement.</p> : null}
    {run.evidence === "timeout" ? <p>The request was accepted, but no matching reply arrived before the bounded timeout. Filtering, resolver behavior, or the path may explain it; this alone cannot establish an internet outage.</p> : null}
    {m?.exchange === "response-received" ? <p>The selected resolver sent a matching DNS reply during this sample. A negative or error reply is still a response; it does not measure packet loss or prove answer correctness.</p> : null}
    {run.evidence === "incomplete" || run.evidence === "transport-failure" ? <p>The exchange did not produce a complete response or timeout sample. Unsent or uncertain requests are not counted as packet loss.</p> : null}
    <dl className="local-quality-facts">
      <div><dt>Historical observer</dt><dd>This controller · {run.interface_name} (index {run.interface_index})</dd></div>
      <div><dt>Query profile</dt><dd>{run.selection.family.toUpperCase()} transport · UDP · {run.selection.query_type} question</dd></div>
      <div><dt>Recorded expectation</dt><dd>{run.selection.expect === "answer" ? "Answer" : run.selection.expect === "nxdomain" ? "NXDOMAIN" : "NODATA"}</dd></div>
      <div><dt>Expectation match at sample time</dt><dd>{run.expectation_matched === undefined ? "Unknown" : run.expectation_matched ? "Matched the recorded expectation" : "Differed from the recorded expectation"}</dd></div>
      <div><dt>Execution outcome</dt><dd>{outcomeCopy[run.outcome]} · separate from DNS result</dd></div>
      <div><dt>Assessment confidence</dt><dd>{run.confidence === "limited" ? "Limited to this historical query sample" : "Unknown"}</dd></div>
      <div><dt>Last retained record</dt><dd><HistoryTime value={run.last_audit_at} /></dd></div>
      <div><dt>Retained records</dt><dd>Approval: {run.authorization_retained ? "retained" : "missing"}; run start: {run.admission_retained ? "retained" : "missing"}; result: {run.terminal_retained ? "retained" : "missing"}</dd></div>
      {m ? <>
        <div><dt>Sample started</dt><dd><HistoryTime value={m.started_at} /></dd></div>
        <div><dt>Sample ended</dt><dd><HistoryTime value={m.completed_at} /></dd></div>
        <div><dt>Request submission</dt><dd>{m.request === "accepted" ? "Accepted by the socket" : m.request === "not-sent" ? "Not sent in this sample" : "Uncertain"}</dd></div>
        {m.rcode !== undefined ? <div><dt>DNS response code</dt><dd>{m.rcode}</dd></div> : null}
        <div><dt>Matched response time</dt><dd>{m.response_time_ns === undefined ? "Unknown" : `${(m.response_time_ns / 1000000).toFixed(3)} ms`}</dd></div>
      </> : null}
      <div><dt>Selection reference</dt><dd>{run.selection.id}</dd></div>
      <div><dt>Resolver reference</dt><dd>{run.selection.resolver_id}</dd></div>
      <div><dt>Query reference</dt><dd>{run.selection.query_id}</dd></div>
      <div><dt>Run reference</dt><dd>{run.run_id}</dd></div>
    </dl>
    <p><strong>Next step:</strong> Compare evidence from the same time and review the original query expectation and resolver policy. A differing reply alone does not prove a broken resolver. Any new check needs separate native approval.</p>
    <p>References identify the original saved selection. Private query names, resolver addresses, and raw answers are not loaded here. The historical interface may differ from the controller's current network.</p>
  </details>;
}
function HistoryTime({ value }: { value: string }) {
  return <time dateTime={value}>{new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "medium" }).format(new Date(value))}</time>;
}
