import { useEffect, useRef, useState } from "react";

import { adminLinkSharesWebCookieHost } from "./adguard-status";
import { loadOPNsenseStatus, type OPNsenseStatus } from "./opnsense-status";

export function OPNsenseStatusPanel({ mode, load = loadOPNsenseStatus, webHostname = window.location.hostname }: { mode: "live" | "demo"; load?: (signal: AbortSignal) => Promise<OPNsenseStatus>; webHostname?: string }) {
  const [status, setStatus] = useState<OPNsenseStatus | null>(null);
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

  return <section className="product-card" aria-labelledby="opnsense-status-title">
    <p className="eyebrow">External router</p>
    <h2 id="opnsense-status-title">OPNsense</h2>
    <p>Read the saved router connection on request. A version response confirms this API read only; it does not prove network coverage or device presence.</p>
    {mode === "demo" ? <p>Connect to the local controller to read a real OPNsense connection.</p> : <>
      <button type="button" className="secondary-action" onClick={() => void read()} disabled={state === "loading"}>{state === "loading" ? "Reading OPNsense status…" : status ? "Refresh OPNsense status" : "Read OPNsense status"}</button>
      {state === "error" ? <p role="alert">OPNsense status is unavailable. Check the router and its protected credential, then try again.</p> : null}
      {status && !status.connected ? <p>No OPNsense router is connected. Connect an owned instance from the native foreground command.</p> : null}
      {status?.connected && status.endpoint ? <>
        <dl className="tools-facts">
          <div><dt>Approved router</dt><dd><code>{status.endpoint}</code></dd></div>
          <div><dt>API version</dt><dd>{status.version}</dd></div>
        </dl>
        <p>Router neighbor reads require a separate foreground review. This candidate still needs an owned-router compatibility and permission lab.</p>
        {adminLinkSharesWebCookieHost(status.endpoint, webHostname) ?
          <p>The router shares this browser session’s host. Open its admin page in a separate browser profile; Cozy SOC withholds the direct link so its local session cookie is not sent to the router.</p> :
          <><a className="secondary-action" href={status.endpoint} target="_blank" rel="noopener noreferrer">Open OPNsense admin UI</a><p>This opens the router’s own page in a new tab. Cozy SOC passes no saved API credential in the link.</p></>}
      </> : null}
    </>}
  </section>;
}
