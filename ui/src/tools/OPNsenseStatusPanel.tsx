import { useEffect, useRef, useState } from "react";

import { adminLinkSharesWebCookieHost } from "./adguard-status";
import { createWebOPNsenseCollectionClient, type OPNsenseCollectionClient, type OPNsenseCollectionReview, type OPNsenseCollectionResult } from "./opnsense-collection";
import { loadOPNsenseStatus, type OPNsenseStatus } from "./opnsense-status";

const defaultCollectionClient = createWebOPNsenseCollectionClient();

export function OPNsenseStatusPanel({ mode, load = loadOPNsenseStatus, collection = defaultCollectionClient, webHostname = window.location.hostname }: { mode: "live" | "demo"; load?: (signal: AbortSignal) => Promise<OPNsenseStatus>; collection?: OPNsenseCollectionClient; webHostname?: string }) {
  const [status, setStatus] = useState<OPNsenseStatus | null>(null);
  const [state, setState] = useState<"idle" | "loading" | "error">("idle");
  const [review, setReview] = useState<OPNsenseCollectionReview | null>(null);
  const [collectionResult, setCollectionResult] = useState<OPNsenseCollectionResult | null>(null);
  const [collectionState, setCollectionState] = useState<"idle" | "loading" | "error">("idle");
  const request = useRef<AbortController | null>(null);
  useEffect(() => () => request.current?.abort(), []);
  useEffect(() => { request.current?.abort(); setStatus(null); setState("idle"); setReview(null); setCollectionResult(null); setCollectionState("idle"); }, [mode]);

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

  const prepareCollection = async () => {
    setCollectionState("loading");
    setCollectionResult(null);
    try { setReview(await collection.review()); setCollectionState("idle"); }
    catch { setCollectionState("error"); }
  };

  const decideCollection = async (approve: boolean) => {
    if (!review) return;
    setCollectionState("loading");
    const pending = review;
    setReview(null);
    try { setCollectionResult(await collection.decide(pending, approve)); setCollectionState("idle"); }
    catch { setCollectionState("error"); }
  };

  return <section className="product-card" aria-labelledby="opnsense-status-title">
    <p className="eyebrow">External router</p>
    <h2 id="opnsense-status-title">OPNsense</h2>
    <p>Read the saved router connection on request. A version response confirms this API read only; it does not prove network coverage or device presence.</p>
    {mode === "demo" ? <p>Connect to the local controller to read a real OPNsense connection.</p> : <>
      <button type="button" className="secondary-action" onClick={() => void read()} disabled={state === "loading" || review !== null}>{state === "loading" ? "Reading OPNsense status…" : status ? "Refresh OPNsense status" : "Read OPNsense status"}</button>
      {state === "error" ? <p role="alert">OPNsense status is unavailable. Check the router and its protected credential, then try again.</p> : null}
      {status && !status.connected ? <p>No OPNsense router is connected. Connect an owned instance from the native foreground command.</p> : null}
      {status?.connected && status.endpoint ? <>
        <dl className="tools-facts">
          <div><dt>Approved router</dt><dd><code>{status.endpoint}</code></dd></div>
          <div><dt>API version</dt><dd>{status.version}</dd></div>
        </dl>
        <p>Router neighbor reads require a separate review and one-use approval. This candidate still needs an owned-router compatibility and permission lab.</p>
        {!review ? <button type="button" className="secondary-action" disabled={collectionState === "loading"} onClick={() => void prepareCollection()}>Review one router neighbor collection</button> : null}
        {review ? <div className="tools-collection-review">
          <h3>Review one router neighbor read</h3>
          <p>Cozy SOC will request the ARP and NDP tables once from <code>{review.endpoint}</code>, with a 2 MiB cap per response, and consider up to {review.max_rows_per_family} rows from each. In-scope IP and hardware addresses become local, short-lived router-reported evidence. No router or network settings change.</p>
          <dl className="tools-facts">
            <div><dt>Enrolled scope</dt><dd><code>{review.scope_id}</code></dd></div>
            <div><dt>Interface</dt><dd>{review.interface.interface_name} (index {review.interface.interface_index})</dd></div>
            <div><dt>Prefixes</dt><dd>{review.interface.prefixes.join(", ")}</dd></div>
            <div><dt>Approval expires</dt><dd>{new Date(review.expires_at).toLocaleString()}</dd></div>
          </dl>
          <p>Router tables can be stale, omit other networks, and cannot establish current device presence, identity, local traffic, or complete coverage.</p>
          <button type="button" className="primary-action" disabled={collectionState === "loading"} onClick={() => void decideCollection(true)}>Approve one read</button>{" "}
          <button type="button" className="secondary-action" disabled={collectionState === "loading"} onClick={() => void decideCollection(false)}>Decline</button>
        </div> : null}
        {collectionState === "loading" ? <p>Working with OPNsense…</p> : null}
        {collectionState === "error" ? <p role="alert">Collection could not be confirmed. Check the current connection and saved router reports before choosing another one-shot read.</p> : null}
        {collectionResult?.outcome === "declined" ? <p>No router neighbor read was approved.</p> : null}
        {collectionResult?.result ? <p>Read {collectionResult.result.read} router rows; saved {collectionResult.result.inserted}, deduplicated {collectionResult.result.deduplicated}, skipped {collectionResult.result.skipped_outside_scope} outside the scope and {collectionResult.result.skipped_duplicate} duplicate rows.{collectionResult.result.ipv4_truncated || collectionResult.result.ipv6_truncated ? " A per-family row limit was reached; earlier rows may be missing." : ""} This is not a coverage measurement.</p> : null}
        {adminLinkSharesWebCookieHost(status.endpoint, webHostname) ?
          <p>The router shares this browser session’s host. Open its admin page in a separate browser profile; Cozy SOC withholds the direct link so its local session cookie is not sent to the router.</p> :
          <><a className="secondary-action" href={status.endpoint} target="_blank" rel="noopener noreferrer">Open OPNsense admin UI</a><p>This opens the router’s own page in a new tab. Cozy SOC passes no saved API credential in the link.</p></>}
      </> : null}
    </>}
  </section>;
}
