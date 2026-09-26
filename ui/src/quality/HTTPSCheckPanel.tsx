import { useEffect, useId, useRef, useState } from "react";
import { SetupRequestError, loadHTTPSSelectionsFromWeb } from "../setup/setup";
import { type HTTPSCheckClient, type HTTPSCheckResult, type HTTPSCheckReview, type HTTPSSelection } from "./https-check";
import "./local-quality.css";

type Phase={kind:"idle"|"loading"|"reviewing"|"running"|"unavailable"|"unknown"}|{kind:"review";review:HTTPSCheckReview}|{kind:"result";result:HTTPSCheckResult};
function approvalDeadline(review:HTTPSCheckReview):number{return Math.min(Date.parse(review.expires_at),Date.parse(review.route_fresh_until))}
export function HTTPSCheckPanel({mode,client,loadSelections=loadHTTPSSelectionsFromWeb}:{mode:"live"|"demo";client:HTTPSCheckClient;loadSelections?:()=>Promise<HTTPSSelection[]>}){
 const titleID=useId(),selectID=useId();
 const [items,setItems]=useState<HTTPSSelection[]|null>(null),[selected,setSelected]=useState(""),[phase,setPhase]=useState<Phase>({kind:"idle"}),[invalid,setInvalid]=useState(false);
 const busy=useRef(false),mounted=useRef(true);
 useEffect(()=>{mounted.current=true;return()=>{mounted.current=false}},[]);
 useEffect(()=>{
  if(phase.kind!=="review")return;
  const invalidate=()=>setInvalid(true);
  const timer=window.setTimeout(invalidate,Math.max(0,approvalDeadline(phase.review)-Date.now()));
  window.addEventListener("blur",invalidate);window.addEventListener("pagehide",invalidate);document.addEventListener("visibilitychange",invalidate);
  return()=>{window.clearTimeout(timer);window.removeEventListener("blur",invalidate);window.removeEventListener("pagehide",invalidate);document.removeEventListener("visibilitychange",invalidate)};
 },[phase]);
 async function load(){
  if(busy.current||phase.kind==="review")return;
  busy.current=true;setPhase({kind:"loading"});
  try{const next=await loadSelections();if(mounted.current){setItems(next);setSelected(next.some((i)=>i.selection_id===selected)?selected:next[0]?.selection_id??"");setPhase({kind:"idle"})}}
  catch{if(mounted.current)setPhase({kind:"unavailable"})}finally{busy.current=false}
 }
 async function review(){
  if(busy.current||!items?.some((i)=>i.selection_id===selected))return;
  busy.current=true;setPhase({kind:"reviewing"});
  try{const next=await client.reviewHTTPS(selected);if(mounted.current){setInvalid(Date.now()>=approvalDeadline(next));setPhase({kind:"review",review:next})}}
  catch{if(mounted.current)setPhase({kind:"unavailable"})}finally{busy.current=false}
 }
 async function decide(approve:boolean){
  if(phase.kind!=="review"||busy.current)return;
  if(approve&&(invalid||Date.now()>=approvalDeadline(phase.review))){setInvalid(true);return}
  const id=phase.review.review_id;busy.current=true;setPhase({kind:"running"});
  try{const result=await client.decideHTTPS(id,approve);if(mounted.current)setPhase({kind:"result",result})}
  catch(error){const noApproval=error instanceof SetupRequestError&&["review_changed","review_expired","cooldown","busy","precondition_failed","check_unavailable"].includes(error.code);
   if(mounted.current)setPhase({kind:!approve?"idle":noApproval?"unavailable":"unknown"})}finally{busy.current=false}
 }
 if(mode==="demo")return <section className="product-card local-quality gateway-check" aria-labelledby={titleID}>
  <p className="eyebrow">Network quality · active check</p><h2 id={titleID}>Check a selected HTTPS target</h2><p>Synthetic demo: HTTPS checks are unavailable. Return to live data to review one.</p>
 </section>;
 return <section className="product-card local-quality gateway-check" aria-labelledby={titleID}>
  <p className="eyebrow">Network quality · active check</p><h2 id={titleID}>Check a selected HTTPS target</h2>
  <p>Use a target saved above. Loading selections reads private HTTPS settings on this device; it sends no request. The controller must be started with the experimental HTTPS-check option.</p>
  <button type="button" disabled={busy.current||phase.kind==="review"} onClick={()=>{void load()}}>Load saved HTTPS selections</button>
  {phase.kind==="loading"?<p role="status">Reading saved HTTPS selections…</p>:null}
  {items?.length===0?<p>No saved HTTPS selection is available for the enrolled network. Save an explicit HTTPS target above first.</p>:null}
  {items&&items.length>0?<form onSubmit={(event)=>{event.preventDefault();void review()}}>
   <label htmlFor={selectID}>Saved HTTPS selection</label>{" "}
   <select id={selectID} value={selected} disabled={busy.current||phase.kind==="review"} onChange={(event)=>setSelected(event.target.value)}>
    {items.map((item)=><option key={item.selection_id} value={item.selection_id}>{item.settings.method} {item.settings.server_name}{item.settings.request_target} via {item.settings.endpoint}</option>)}
   </select>{" "}<button type="submit" className="primary-action" disabled={busy.current||phase.kind==="review"}>Review one-shot HTTPS check</button>
  </form>:null}
  {phase.kind==="reviewing"?<p role="status">Checking current route and source without sending an HTTPS request…</p>:null}
  {phase.kind==="unavailable"?<p role="alert">A fresh selection, review or run is unavailable. Check enrollment and the controller connection. A pending review may take up to 30 seconds to expire. No check was approved.</p>:null}
  {phase.kind==="review"?<div aria-label="HTTPS check review">
   <p><strong>Review before sending:</strong> this is one selected numeric endpoint with an explicit TLS identity and exact HTTP request. The controller will recheck the route, source and settings before sending.</p>
   <dl className="local-quality-facts">
    <div><dt>Endpoint</dt><dd>{phase.review.settings.endpoint}</dd></div>
    <div><dt>TLS identity and Host</dt><dd>{phase.review.settings.server_name}</dd></div>
    <div><dt>Expected response</dt><dd>HTTP {phase.review.settings.expected_status} for {phase.review.settings.method} {phase.review.settings.request_target}</dd></div>
    <div><dt>Destination policy</dt><dd>{phase.review.settings.destination_policy}{phase.review.outside_enrolled_prefixes?" · endpoint outside enrolled prefixes":" · endpoint inside enrolled prefixes"}</dd></div>
    <div><dt>Source</dt><dd>{phase.review.source} via {phase.review.interface_name} (index {phase.review.interface_index})</dd></div>
    <div><dt>Enrolled prefixes</dt><dd>{phase.review.prefixes.join(", ")}</dd></div>
    <div><dt>Route evidence</dt><dd>Observed <time dateTime={phase.review.route_observed_at}>{new Date(phase.review.route_observed_at).toLocaleTimeString()}</time>; fresh until <time dateTime={phase.review.route_fresh_until}>{new Date(phase.review.route_fresh_until).toLocaleTimeString()}</time></dd></div>
    <div><dt>TLS and HTTP policy</dt><dd>{phase.review.policy.min_tls_version}–{phase.review.policy.max_tls_version}; {phase.review.policy.trust_store} trust and server identity verification; {phase.review.policy.alpn} ALPN; {phase.review.policy.http_version}; fresh connection; no client authentication, session resumption, early data, proxy, name resolution, redirects or response body read</dd></div>
    <div><dt>Traffic ceiling</dt><dd>{phase.review.budget.max_connections} connection, {phase.review.budget.max_requests} request, {phase.review.budget.max_retries} retries; {phase.review.budget.max_request_bytes} request bytes; {phase.review.budget.max_response_header_bytes} response-header bytes; {phase.review.budget.max_transport_read_bytes} transport read bytes and {phase.review.budget.max_transport_write_bytes} transport write bytes; {phase.review.budget.max_transport_read_calls} reads and {phase.review.budget.max_transport_write_calls} writes</dd></div>
    <div><dt>Time limits</dt><dd>{phase.review.budget.connect_timeout_ms} ms connect, {phase.review.budget.tls_handshake_timeout_ms} ms TLS, {phase.review.budget.response_header_timeout_ms} ms response headers, {phase.review.budget.total_timeout_ms} ms total; {phase.review.budget.max_concurrent_runs} run at a time and at least {phase.review.budget.min_run_interval_ms/1000} seconds between runs</dd></div>
    <div><dt>Review expires</dt><dd><time dateTime={phase.review.expires_at}>{new Date(phase.review.expires_at).toLocaleTimeString()}</time></dd></div>
   </dl>
   <p><strong>Exact HTTP request bytes (escaped, including CRLF):</strong></p><pre className="https-check-request"><code>{JSON.stringify(phase.review.request_bytes)}</code></pre>
   <p><strong>Privacy and data-use limits:</strong></p><ul>{phase.review.privacy.map((note)=><li key={note}>{note}</li>)}</ul>
   <p>The destination can observe this check; TLS and transport byte limits are not a billing estimate. This one sample does not enable ongoing monitoring.</p>
   {invalid?<p role="alert">This review or its route evidence expired, or the page lost focus. Approval is disabled. Decline it before starting a fresh review.</p>:null}
   <button type="button" className="primary-action" disabled={invalid} onClick={()=>{void decide(true)}}>Approve one HTTPS check</button>{" "}
   <button type="button" onClick={()=>{void decide(false)}}>Decline check</button>
  </div>:null}
  {phase.kind==="running"?<p role="status">Submitting this one approval and waiting for the audited result…</p>:null}
  {phase.kind==="unknown"?<p role="alert">The result could not be confirmed after approval. HTTPS traffic may have been sent. Inspect saved HTTPS history; do not automatically retry.</p>:null}
  {phase.kind==="result"?<p role="status">{phase.result.outcome==="declined"?"Check declined. No HTTPS request was approved.":<>Check outcome: {phase.result.outcome}. Run reference: {phase.result.run_id}. Read saved HTTPS history for the response and audit context. Another check requires a new review and approval.</>}</p>:null}
  <p className="local-quality-limits">A single response or timeout does not prove internet health, security or full network coverage. An unexpected HTTP status is still a received response, not packet loss.</p>
 </section>;
}
