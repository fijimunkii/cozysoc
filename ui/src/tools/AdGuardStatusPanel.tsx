import { useEffect, useRef, useState } from "react";

import { adminLinkSharesWebCookieHost, loadAdGuardStatus, type AdGuardStatus } from "./adguard-status";

export function AdGuardStatusPanel({ mode, load = loadAdGuardStatus, webHostname = window.location.hostname }: { mode: "live" | "demo"; load?: (signal: AbortSignal) => Promise<AdGuardStatus>; webHostname?: string }) {
  const [status, setStatus] = useState<AdGuardStatus | null>(null);
  const [state, setState] = useState<"idle" | "loading" | "error">("idle");
  const request = useRef<AbortController | null>(null);
  useEffect(() => () => request.current?.abort(), []);
  useEffect(() => { request.current?.abort(); setStatus(null); setState("idle"); }, [mode]);

  const read = async () => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setStatus(null);
    setState("loading");
    try {
      const result = await load(controller.signal);
      if (!controller.signal.aborted) { setStatus(result); setState("idle"); }
    } catch {
      if (!controller.signal.aborted) setState("error");
    }
  };

  return <section className="product-card" aria-labelledby="adguard-status-title">
    <p className="eyebrow">External resolver</p>
    <h2 id="adguard-status-title">AdGuard Home</h2>
    <p>Read the connected instance’s service state on request. This does not read DNS query history, change its settings, or prove any client is using it.</p>
    {mode === "demo" ? <p>Connect to the local controller to read a real AdGuard Home connection.</p> : <>
      <button type="button" className="secondary-action" onClick={() => void read()} disabled={state === "loading"}>{state === "loading" ? "Reading AdGuard Home status…" : status ? "Refresh AdGuard Home status" : "Read AdGuard Home status"}</button>
      {state === "error" ? <p role="alert">AdGuard Home status is unavailable. Check the instance and its local credential, then try again.</p> : null}
      {status && !status.connected ? <p>No AdGuard Home instance is connected. Connect an existing instance from the native foreground command.</p> : null}
      {status?.connected ? <>
        <dl className="tools-facts">
          <div><dt>Instance</dt><dd><code>{status.endpoint}</code></dd></div>
          <div><dt>Version</dt><dd>{status.version}</dd></div>
          <div><dt>Service</dt><dd>{status.running ? "Running" : "Not running"}</dd></div>
          <div><dt>Protection setting</dt><dd>{status.protection_enabled ? "Enabled" : "Disabled"}</dd></div>
          <div><dt>Filtering setting</dt><dd>{status.filtering_enabled ? "Enabled" : "Disabled"}</dd></div>
          <div><dt>Query log setting</dt><dd>{status.query_log_enabled ? "Enabled" : "Disabled"}</dd></div>
          <div><dt>Client IP anonymization</dt><dd>{status.anonymized_clients ? "Enabled" : "Disabled"}</dd></div>
        </dl>
        <p>These settings describe the resolver, not household coverage. Clients using another resolver or encrypted DNS may be unobserved.</p>
        {status.endpoint && adminLinkSharesWebCookieHost(status.endpoint, webHostname) ?
          <p>The admin page shares this browser session’s host. Open it in a separate browser profile; Cozy SOC withholds the direct link so its local session cookie is not sent to the other service.</p> :
          <><a className="secondary-action" href={status.endpoint} target="_blank" rel="noopener noreferrer">Open AdGuard Home admin UI</a><p>The link opens the external owner’s page in a new tab. Cozy SOC does not pass its local session or saved credentials to that page.</p></>}
      </> : null}
    </>}
  </section>;
}
