import { useEffect, useState } from "react";
import { loadArrivalFindings, type ArrivalFindingList } from "./arrivals";
import "./arrivals.css";

export function ArrivalFindingsPage({ mode, onNavigate }: { mode: "live" | "demo"; onNavigate: (page: "devices" | "activity") => void }) {
  const [state, setState] = useState<{ status: "loading" | "ready" | "failed"; data?: ArrivalFindingList }>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (mode !== "live") return;
    const controller = new AbortController();
    setState({ status: "loading" });
    loadArrivalFindings(controller.signal).then((data) => {
      if (!controller.signal.aborted) setState({ status: "ready", data });
    }).catch(() => {
      if (!controller.signal.aborted) setState({ status: "failed" });
    });
    return () => controller.abort();
  }, [mode, attempt]);

  if (mode === "demo") return <section className="product-card empty-product-state"><h2>Arrival findings are live evidence only</h2><p>The synthetic demo has no findings. Connect to a local controller to read retained Device Watch arrivals.</p></section>;
  return <div className="arrival-findings-page">
    <section className="product-card">
      <h2>Informational arrival findings</h2>
      <p>Each item records a newly observed network identity from Device Watch's passive neighbor cache. A returning device with a changed address, an observation gap, or incomplete visibility can look new. These items are not security verdicts or desktop notifications.</p>
      <p>Next step: review Devices and Activity around the recorded time. Confirm identity from context you trust before changing a label; an arrival alone calls for no network action.</p>
      <div className="arrival-findings-actions"><button type="button" className="secondary-action" onClick={() => onNavigate("devices")}>Review devices</button><button type="button" className="secondary-action" onClick={() => onNavigate("activity")}>Review activity</button></div>
    </section>
    {state.status === "loading" ? <section className="product-card" role="status">Reading retained findings…</section> : null}
    {state.status === "failed" ? <section className="product-card" role="alert"><h2>Findings are temporarily unavailable</h2><p>Other local evidence remains available.</p><button type="button" className="primary-action" onClick={() => setAttempt((value) => value + 1)}>Retry findings</button></section> : null}
    {state.status === "ready" && state.data ? <section className="product-card">
      <p>Read {formatTime(state.data.as_of)}. Only the newest 100 retained Device Watch arrivals can appear here. Source observations may expire independently.</p>
      {state.data.truncated ? <p role="status">Older arrival findings are outside this bounded view.</p> : null}
      {state.data.items.length === 0 ? <p>No retained arrival findings. This does not prove that no devices joined or that the network is fully observed.</p> :
        <ol className="arrival-findings-list">{state.data.items.map((item) => <li key={item.id}>
          <h3>New network identity observed</h3>
          <p>Recorded {formatTime(item.recorded_at)}. Source observation time: {formatTime(item.observed_at)}.</p>
          <p>{item.evidence_retained ? "The source observation is still retained." : "The source observation has expired or is unavailable; this finding remains historical."}</p>
          <dl><div><dt>Finding</dt><dd><code>{item.id}</code></dd></div><div><dt>Network scope</dt><dd><code>{item.scope_id}</code></dd></div><div><dt>Source observation</dt><dd><code>{item.evidence_observation_id}</code></dd></div></dl>
        </li>)}</ol>}
    </section> : null}
  </div>;
}

function formatTime(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}
