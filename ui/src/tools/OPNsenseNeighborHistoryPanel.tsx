import { useEffect, useRef, useState } from "react";

import { loadOPNsenseNeighborHistory, type OPNsenseNeighborHistory } from "./opnsense-neighbors";

export function OPNsenseNeighborHistoryPanel({ mode, load = loadOPNsenseNeighborHistory }: { mode: "live" | "demo"; load?: (signal: AbortSignal) => Promise<OPNsenseNeighborHistory> }) {
  const [history, setHistory] = useState<OPNsenseNeighborHistory | null>(null);
  const [state, setState] = useState<"idle" | "loading" | "error">("idle");
  const request = useRef<AbortController | null>(null);
  useEffect(() => () => request.current?.abort(), []);
  useEffect(() => { request.current?.abort(); setHistory(null); setState("idle"); }, [mode]);

  const read = async () => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setHistory(null);
    setState("loading");
    try {
      const result = await load(controller.signal);
      if (!controller.signal.aborted) { setHistory(result); setState("idle"); }
    } catch {
      if (!controller.signal.aborted) setState("error");
    }
  };

  return <section className="product-card" aria-labelledby="opnsense-neighbors-title">
    <p className="eyebrow">Router evidence</p>
    <h2 id="opnsense-neighbors-title">Recent OPNsense neighbor reports</h2>
    <p>Read locally retained ARP/NDP reports from the enrolled network’s last 24 hours. These are router table entries captured when you approved a collection, not verified device identity, current presence, or monitoring coverage.</p>
    {mode === "demo" ? <p>Connect to the local controller to inspect retained router reports.</p> : <>
      <button type="button" className="secondary-action" onClick={() => void read()} disabled={state === "loading"}>{state === "loading" ? "Reading router reports…" : history ? "Refresh router reports" : "Read router reports"}</button>
      {state === "error" ? <p role="alert">Router reports are unavailable. Check local evidence storage and try again.</p> : null}
      {history && !history.scope_enrolled ? <p>No network scope is enrolled for router reports.</p> : null}
      {history?.scope_enrolled ? <>
        <p>Scope <code>{history.scope_id}</code> · read {new Date(history.as_of).toLocaleString()}.</p>
        {history.reports.length === 0 ? <p>No retained router reports were found. Silence does not prove that no devices are present.</p> :
          <ul className="tool-list">{history.reports.map((report) => <li key={report.observation_id}>
            <strong>{report.address} <small>({report.family.toUpperCase()})</small></strong>
            <span>{report.hardware_address} · {report.interface} · captured {new Date(report.captured_at).toLocaleString()} · evidence <code>{report.observation_id}</code></span>
          </li>)}</ul>}
        {history.truncated ? <p>Showing the newest 100 retained reports. Older reports may be omitted.</p> : null}
        <p>ARP/NDP tables can be stale and can omit other networks or local traffic. They do not establish device-level traffic coverage.</p>
      </> : null}
    </>}
  </section>;
}
