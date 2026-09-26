import { useEffect, useRef, useState } from "react";

import { adminLinkSharesWebCookieHost, loadAdGuardStatus, type AdGuardStatus } from "./adguard-status";
import { createWebAdGuardCollectionClient, type AdGuardCollectionClient, type AdGuardCollectionReview, type AdGuardCollectionResult } from "./adguard-collection";

const defaultCollectionClient = createWebAdGuardCollectionClient();

export function AdGuardStatusPanel({ mode, load = loadAdGuardStatus, collection = defaultCollectionClient, webHostname = window.location.hostname }: { mode: "live" | "demo"; load?: (signal: AbortSignal) => Promise<AdGuardStatus>; collection?: AdGuardCollectionClient; webHostname?: string }) {
  const [status, setStatus] = useState<AdGuardStatus | null>(null);
  const [state, setState] = useState<"idle" | "loading" | "error">("idle");
  const [review, setReview] = useState<AdGuardCollectionReview | null>(null);
  const [collectionResult, setCollectionResult] = useState<AdGuardCollectionResult | null>(null);
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

  return <section className="product-card" aria-labelledby="adguard-status-title">
    <p className="eyebrow">External resolver</p>
    <h2 id="adguard-status-title">AdGuard Home</h2>
    <p>Read the connected instance’s service state on request. This does not read DNS query history, change its settings, or prove any client is using it.</p>
    {mode === "demo" ? <p>Connect to the local controller to read a real AdGuard Home connection.</p> : <>
      <button type="button" className="secondary-action" onClick={() => void read()} disabled={state === "loading" || review !== null}>{state === "loading" ? "Reading AdGuard Home status…" : status ? "Refresh AdGuard Home status" : "Read AdGuard Home status"}</button>
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
        <div className="adguard-filter-sources">
          <h3>Filter lists reported by AdGuard Home</h3>
          {status.filter_inventory ? <>
            <p>{status.filter_inventory.blocklist_total} blocklists and {status.filter_inventory.allowlist_total} allowlists reported by this instance.{status.filter_inventory.truncated ? " Showing up to 32 of each kind." : ""} List metadata does not prove any client used filtering.</p>
            {status.filter_inventory.sources.length > 0 ? <ul className="tool-list">{status.filter_inventory.sources.map((source) => <li key={`${source.kind}-${source.id}`}>
              <strong>{source.name}</strong>
              <span>{source.kind === "blocklist" ? "Blocklist" : "Allowlist"} #{source.id} · {source.enabled ? "Enabled" : "Disabled"} · {source.rules_count} rules{source.last_updated ? ` · Updated ${new Date(source.last_updated).toLocaleString()} (resolver reported)` : " · Update time unavailable"}</span>
            </li>)}</ul> : <p>No filter lists were reported.</p>}
            <p>Source URLs and custom rule text stay out of this view. The reported update time is not a verified source version.</p>
          </> : <p>Filter list details are unavailable from this status read. The filtering setting above does not identify active list sources.</p>}
        </div>
        {status.running && status.query_log_enabled && !review ? <button type="button" className="secondary-action" disabled={collectionState === "loading"} onClick={() => void prepareCollection()}>Review one DNS history collection</button> : null}
        {review ? <div className="adguard-collection-review">
          <h3>Review one private DNS history read</h3>
          <p>Cozy SOC will read up to {review.max_queries} queries from the last {review.max_query_age_hours} hours from <code>{review.endpoint}</code> and keep only visible client IPs in the enrolled scope. Saved observations follow local ephemeral retention. No DNS or network settings change.</p>
          <dl className="tools-facts">
            <div><dt>Enrolled scope</dt><dd><code>{review.scope_id}</code></dd></div>
            <div><dt>Interface</dt><dd>{review.interface.interface_name} (index {review.interface.interface_index})</dd></div>
            <div><dt>Prefixes</dt><dd>{review.interface.prefixes.join(", ")}</dd></div>
            <div><dt>Approval expires</dt><dd>{new Date(review.expires_at).toLocaleString()}</dd></div>
          </dl>
          <p>Only queries that reached this resolver can appear. Results do not establish DNS coverage or a device identity.</p>
          <button type="button" className="primary-action" disabled={collectionState === "loading"} onClick={() => void decideCollection(true)}>Approve one read</button>{" "}
          <button type="button" className="secondary-action" disabled={collectionState === "loading"} onClick={() => void decideCollection(false)}>Decline</button>
        </div> : null}
        {collectionState === "loading" ? <p>Working with AdGuard Home…</p> : null}
        {collectionState === "error" ? <p role="alert">Collection could not be confirmed. Check the current connection and saved device history before choosing another one-shot read.</p> : null}
        {collectionResult?.outcome === "declined" ? <p>No DNS history read was approved.</p> : null}
        {collectionResult?.result ? <p>Read {collectionResult.result.read} recent queries; saved {collectionResult.result.inserted}, deduplicated {collectionResult.result.deduplicated}, skipped {collectionResult.result.skipped_outside_scope} outside the scope, {collectionResult.result.skipped_without_client_ip} without a visible client IP, and {collectionResult.result.skipped_outside_window} outside the retention window.{collectionResult.result.limit_reached ? " The 100-query limit was reached; earlier history may be missing." : ""} This is not a coverage measurement.</p> : null}
        {status.endpoint && adminLinkSharesWebCookieHost(status.endpoint, webHostname) ?
          <p>The admin page shares this browser session’s host. Open it in a separate browser profile; Cozy SOC withholds the direct link so its local session cookie is not sent to the other service.</p> :
          <><a className="secondary-action" href={status.endpoint} target="_blank" rel="noopener noreferrer">Open AdGuard Home admin UI</a><p>The link opens the external owner’s page in a new tab. Cozy SOC does not pass its local session or saved credentials to that page.</p></>}
      </> : null}
    </>}
  </section>;
}
