import { useEffect, useId, useRef, useState } from "react";
import { SetupRequestError, loadResolverSelectionsFromWeb } from "../setup/setup";
import { type ResolverCheckClient, type ResolverCheckResult, type ResolverReview, type ResolverSelection } from "./resolver-check";
import "./local-quality.css";

type Phase = { kind:"idle" | "loading" | "reviewing" | "running" | "unavailable" | "unknown" } |
  { kind:"review"; review:ResolverReview } | { kind:"result"; result:ResolverCheckResult };
export function ResolverCheckPanel({ mode, client, loadSelections = loadResolverSelectionsFromWeb }: {
  mode:"live" | "demo"; client:ResolverCheckClient; loadSelections?:()=>Promise<ResolverSelection[]>;
}) {
  const titleID=useId(), selectID=useId();
  const [items,setItems]=useState<ResolverSelection[]|null>(null);
  const [selected,setSelected]=useState("");
  const [phase,setPhase]=useState<Phase>({kind:"idle"});
  const [invalid,setInvalid]=useState(false);
  const busy=useRef(false), mounted=useRef(true);
  useEffect(()=>{ mounted.current=true; return ()=>{mounted.current=false;} },[]);
  useEffect(()=>{
    if (phase.kind!=="review") return;
    const invalidate=()=>setInvalid(true);
    const timer=window.setTimeout(invalidate,Math.max(0,Date.parse(phase.review.expires_at)-Date.now()));
    window.addEventListener("blur",invalidate); window.addEventListener("pagehide",invalidate); document.addEventListener("visibilitychange",invalidate);
    return ()=>{window.clearTimeout(timer); window.removeEventListener("blur",invalidate); window.removeEventListener("pagehide",invalidate); document.removeEventListener("visibilitychange",invalidate);};
  },[phase]);
  async function load() {
    if (busy.current || phase.kind==="review") return;
    busy.current=true; setPhase({kind:"loading"});
    try {
      const next=await loadSelections();
      if (mounted.current) { setItems(next); setSelected(next.some((i)=>i.selection_id===selected)?selected:next[0]?.selection_id??""); setPhase({kind:"idle"}); }
    } catch { if (mounted.current) setPhase({kind:"unavailable"}); }
    finally { busy.current=false; }
  }
  async function review() {
    if (busy.current || !items?.some((i)=>i.selection_id===selected)) return;
    busy.current=true; setPhase({kind:"reviewing"});
    try {
      const next=await client.reviewResolver(selected);
      if (mounted.current) { setInvalid(Date.now()>=Date.parse(next.expires_at)); setPhase({kind:"review",review:next}); }
    } catch { if (mounted.current) setPhase({kind:"unavailable"}); }
    finally { busy.current=false; }
  }
  async function decide(approve:boolean) {
    if (phase.kind!=="review" || busy.current) return;
    if (approve && (invalid || Date.now()>=Date.parse(phase.review.expires_at))) { setInvalid(true); return; }
    const id=phase.review.review_id;
    busy.current=true; setPhase({kind:"running"});
    try { const result=await client.decideResolver(id,approve); if (mounted.current) setPhase({kind:"result",result}); }
    catch(error) {
      const noApproval=error instanceof SetupRequestError && ["review_changed","review_expired","cooldown","busy","precondition_failed","check_unavailable"].includes(error.code);
      if (mounted.current) setPhase({kind:!approve?"idle":noApproval?"unavailable":"unknown"});
    } finally { busy.current=false; }
  }
  if (mode==="demo") return <section className="product-card local-quality gateway-check" aria-labelledby={titleID}>
    <p className="eyebrow">Network quality · active check</p><h2 id={titleID}>Check a selected DNS resolver</h2>
    <p>Synthetic demo: resolver checks are unavailable. Return to live data to review one.</p>
  </section>;
  return <section className="product-card local-quality gateway-check" aria-labelledby={titleID}>
    <p className="eyebrow">Network quality · active check</p><h2 id={titleID}>Check a selected DNS resolver</h2>
    <p>Use a resolver selection saved through the local controller. Loading saved selections reads private DNS settings on this device; it sends no query. The controller must be started with the experimental resolver-check option.</p>
    <button type="button" disabled={busy.current || phase.kind==="review"} onClick={()=>{void load();}}>Load saved resolver selections</button>
    {phase.kind==="loading" ? <p role="status">Reading saved selections…</p> : null}
    {items?.length===0 ? <p>No saved selection is available for the enrolled network. Save explicit resolver settings with the local command first.</p> : null}
    {items && items.length>0 ? <form onSubmit={(event)=>{event.preventDefault();void review();}}>
      <label htmlFor={selectID}>Saved resolver selection</label>{" "}
      <select id={selectID} value={selected} disabled={busy.current || phase.kind==="review"} onChange={(event)=>setSelected(event.target.value)}>
        {items.map((item)=><option key={item.selection_id} value={item.selection_id}>{item.settings.endpoint} · {item.settings.query_type} {item.settings.name}</option>)}
      </select>{" "}
      <button type="submit" className="primary-action" disabled={busy.current || phase.kind==="review"}>Review one-shot DNS check</button>
    </form> : null}
    {phase.kind==="reviewing" ? <p role="status">Checking current resolver binding without sending a DNS question…</p> : null}
    {phase.kind==="unavailable" ? <p role="alert">A fresh selection, review or run is unavailable. Check enrollment and the controller connection. A pending review may take up to 30 seconds to expire. No check was approved.</p> : null}
    {phase.kind==="review" ? <div aria-label="DNS resolver check review">
      <p><strong>Review before sending:</strong> this is one selected resolver and one exact DNS question. The controller will recheck the selection and source before sending.</p>
      <dl className="local-quality-facts">
        <div><dt>Resolver endpoint</dt><dd>{phase.review.settings.endpoint}</dd></div>
        <div><dt>Exact question</dt><dd>{phase.review.settings.query_type} {phase.review.settings.name}; expected {phase.review.settings.expect}</dd></div>
        <div><dt>Destination policy</dt><dd>{phase.review.settings.destination_scope}{phase.review.outside_enrolled_prefixes ? " · endpoint outside enrolled prefixes" : " · endpoint inside enrolled prefixes"}</dd></div>
        <div><dt>Source</dt><dd>{phase.review.source} via {phase.review.interface_name} (index {phase.review.interface_index})</dd></div>
        <div><dt>Enrolled prefixes</dt><dd>{phase.review.prefixes.join(", ")}</dd></div>
        <div><dt>Traffic ceiling</dt><dd>At most {phase.review.budget.max_send_calls} UDP DNS question, {phase.review.budget.max_request_bytes} DNS request bytes, {phase.review.budget.max_reply_bytes} reply bytes, {phase.review.budget.max_received_datagrams} received datagrams and {phase.review.budget.max_receive_calls} receive calls</dd></div>
        <div><dt>Time limits</dt><dd>{phase.review.budget.exchange_timeout_ms} ms exchange, {phase.review.budget.total_timeout_ms} ms total, {phase.review.budget.max_concurrent_runs} run at a time, at least {phase.review.budget.min_run_interval_ms/1000} seconds between runs</dd></div>
        <div><dt>Review expires</dt><dd><time dateTime={phase.review.expires_at}>{new Date(phase.review.expires_at).toLocaleTimeString()}</time></dd></div>
      </dl>
      <p>The selected resolver may forward this exact query upstream, even when it is inside your enrolled network. DNS byte ceilings exclude IP/link overhead and all server-side traffic. This check is locally audited and does not enable ongoing monitoring.</p>
      {invalid ? <p role="alert">This review expired or the page lost focus. Approval is disabled. Decline it before starting a fresh review.</p> : null}
      <button type="button" className="primary-action" disabled={invalid} onClick={()=>{void decide(true);}}>Approve one DNS check</button>{" "}
      <button type="button" onClick={()=>{void decide(false);}}>Decline check</button>
    </div> : null}
    {phase.kind==="running" ? <p role="status">Submitting this one approval and waiting for the audited result…</p> : null}
    {phase.kind==="unknown" ? <p role="alert">The result could not be confirmed after approval. DNS traffic may have been sent. Inspect saved DNS history; do not automatically retry.</p> : null}
    {phase.kind==="result" ? <p role="status">{phase.result.outcome==="declined" ? "Check declined. No DNS question was approved." : <>Check outcome: {phase.result.outcome}. Run reference: {phase.result.run_id}. Read saved DNS history for the measurement and audit context. Another check requires a new review and approval.</>}</p> : null}
    <p className="local-quality-limits">One resolver reply or timeout does not prove internet health, security, or full network coverage. This is an experimental one-shot check of only the selected question and endpoint.</p>
  </section>;
}
