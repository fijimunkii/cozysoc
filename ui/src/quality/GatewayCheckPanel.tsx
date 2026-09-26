import { useEffect, useRef, useState } from "react";
import { type GatewayCheckClient, type GatewayCheckResult, type GatewayCheckReview } from "./gateway-check";
import { SetupRequestError } from "../setup/setup";
import "./local-quality.css";

type Phase = { kind: "idle" | "reviewing" | "unavailable" | "running" | "unknown" } |
  { kind: "review"; review: GatewayCheckReview } | { kind: "result"; result: GatewayCheckResult };

export function GatewayCheckPanel({ mode, client }: { mode: "live" | "demo"; client: GatewayCheckClient }) {
  if (mode === "demo") return <section className="product-card local-quality gateway-check" aria-labelledby="gateway-check-title">
    <p className="eyebrow">Network quality · active check</p><h2 id="gateway-check-title">Check a selected gateway</h2>
    <p>Synthetic demo: gateway checks are unavailable. Return to live data to review a one-shot check.</p>
  </section>;
  return <LiveGatewayCheckPanel client={client} />;
}

function LiveGatewayCheckPanel({ client }: { client: GatewayCheckClient }) {
  const [target, setTarget] = useState("");
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });
  const [reviewInvalid, setReviewInvalid] = useState(false);
  const busy = useRef(false);
  const mounted = useRef(true);

  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);
  useEffect(() => {
    if (phase.kind !== "review") return;
    const invalidate = () => setReviewInvalid(true);
    const delay = Math.max(0, Date.parse(phase.review.expires_at) - Date.now());
    const timer = window.setTimeout(invalidate, delay);
    window.addEventListener("blur", invalidate);
    window.addEventListener("pagehide", invalidate);
    document.addEventListener("visibilitychange", invalidate);
    return () => {
      window.clearTimeout(timer);
      window.removeEventListener("blur", invalidate);
      window.removeEventListener("pagehide", invalidate);
      document.removeEventListener("visibilitychange", invalidate);
    };
  }, [phase]);

  async function review() {
    if (busy.current || !/^(?:0|[1-9]\d{0,2})(?:\.(?:0|[1-9]\d{0,2})){3}$/.test(target)) return;
    busy.current = true;
    setPhase({ kind: "reviewing" });
    try {
      const value = await client.reviewGateway(target);
      if (mounted.current) {
        setReviewInvalid(Date.now() >= Date.parse(value.expires_at));
        setPhase({ kind: "review", review: value });
      }
    } catch {
      if (mounted.current) setPhase({ kind: "unavailable" });
    } finally { busy.current = false; }
  }

  async function decide(approve: boolean) {
    if (phase.kind !== "review" || busy.current) return;
    if (approve && (reviewInvalid || Date.now() >= Date.parse(phase.review.expires_at))) {
      setReviewInvalid(true); return;
    }
    busy.current = true;
    const id = phase.review.review_id;
    setPhase({ kind: "running" });
    try {
      const result = await client.decideGateway(id, approve);
      if (mounted.current) setPhase({ kind: "result", result });
    } catch (error) {
      // A request can fail after approval and after packets were sent. Never retry it automatically.
      const noApproval = error instanceof SetupRequestError && ["review_changed", "review_expired", "cooldown", "busy", "precondition_failed", "check_unavailable"].includes(error.code);
      if (mounted.current) setPhase({ kind: !approve ? "idle" : noApproval ? "unavailable" : "unknown" });
    } finally { busy.current = false; }
  }

  return <section className="product-card local-quality gateway-check" aria-labelledby="gateway-check-title">
    <p className="eyebrow">Network quality · active check</p><h2 id="gateway-check-title">Check a selected gateway</h2>
    <p>Enter a numeric private IPv4 address on your enrolled network. Review the exact source and limits before choosing to send any traffic. The controller must be started with the experimental gateway-check option.</p>
    <form onSubmit={(event) => { event.preventDefault(); void review(); }}>
      <label htmlFor="gateway-target">Selected gateway address</label>{" "}
      <input id="gateway-target" type="text" inputMode="decimal" autoComplete="off" spellCheck={false} maxLength={15} value={target}
        disabled={busy.current || phase.kind === "review"} onChange={(event) => setTarget(event.target.value)} />{" "}
      <button type="submit" className="primary-action" disabled={busy.current || phase.kind === "review"}>Review one-shot check</button>
    </form>
    {phase.kind === "reviewing" ? <p role="status">Checking local route and source without sending a probe…</p> : null}
    {phase.kind === "unavailable" ? <p role="alert">A fresh review or run is unavailable. Check enrollment, the route and controller connection; a pending review may take up to 30 seconds to expire. No check was approved.</p> : null}
    {phase.kind === "review" ? <div aria-label="Gateway check review">
      <p><strong>Review before sending:</strong> the controller selected these details at one point in time. The destination has not been verified as your gateway.</p>
      <dl className="local-quality-facts">
        <div><dt>Destination</dt><dd>{phase.review.target}</dd></div>
        <div><dt>Source</dt><dd>{phase.review.source} via {phase.review.interface_name} (index {phase.review.interface_index})</dd></div>
        <div><dt>Enrolled prefixes</dt><dd>{phase.review.prefixes.join(", ")}</dd></div>
        <div><dt>Traffic ceiling</dt><dd>Up to {phase.review.budget.max_attempts} ICMP echo requests, {phase.review.budget.payload_bytes} payload bytes each, up to {phase.review.budget.max_icmp_request_bytes} request bytes each excluding IP and link overhead; at least {phase.review.budget.min_interval_ms} ms apart</dd></div>
        <div><dt>Time limits</dt><dd>{phase.review.budget.attempt_timeout_ms} ms per attempt; {phase.review.budget.total_timeout_ms} ms total; one run at a time and at least {phase.review.budget.min_run_interval_ms / 1000} seconds between runs</dd></div>
        <div><dt>Review expires</dt><dd><time dateTime={phase.review.expires_at}>{new Date(phase.review.expires_at).toLocaleTimeString()}</time></dd></div>
      </dl>
      <p>This active check may be visible to the destination and network infrastructure. The controller keeps an audit and measurement history locally. It does not enable ongoing monitoring. Route and source are rechecked before sending; a changed review is rejected.</p>
      {reviewInvalid ? <p role="alert">This review expired or the page lost focus. Approval is disabled. Start a fresh review after declining this one.</p> : null}
      <button type="button" className="primary-action" disabled={reviewInvalid} onClick={() => { void decide(true); }}>Approve one check to {phase.review.target}</button>{" "}
      <button type="button" onClick={() => { void decide(false); }}>Decline check</button>
    </div> : null}
    {phase.kind === "running" ? <p role="status">Submitting this one approval and waiting for the audited result…</p> : null}
    {phase.kind === "unknown" ? <p role="alert">The result could not be confirmed after approval. Traffic may have been sent. Inspect saved gateway history; do not automatically retry.</p> : null}
    {phase.kind === "result" ? <ResultDetails result={phase.result} /> : null}
    <p className="local-quality-limits">A reply measures only this selected target during this check. Missing replies alone do not prove a gateway, DNS, internet or security failure. This check does not expand monitoring coverage.</p>
  </section>;
}

function ResultDetails({ result }: { result: GatewayCheckResult }) {
  if (result.outcome === "declined") return <p role="status">Check declined. No active check was approved.</p>;
  const m = result.measurement;
  return <div role="status">
    <p><strong>Check outcome:</strong> {result.outcome}. This is execution state, not an internet or security verdict.</p>
    {m ? <p>{m.complete ? "Complete sample" : "Partial sample"}: {m.accepted_requests} accepted requests, {m.replies} replies, {m.timeouts} timeouts. {m.mean_rtt_ns === undefined ? "Mean round-trip time unknown." : `Mean round-trip time ${(m.mean_rtt_ns / 1000000).toFixed(3)} ms.`}</p> : <p>No usable connectivity measurement was returned.</p>}
    <p>Run reference: {result.run_id}. Read saved gateway history for audit context. Another check requires a separate review and approval.</p>
  </div>;
}
