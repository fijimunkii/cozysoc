import { useEffect, useRef, useState } from "react";
import { loadLocalQualityFromWeb, type LocalQualityLoader, type QualityGap } from "./local-quality";
import { qualityExpired, sampleClock, stampQuality, type QualityReceipt } from "./freshness";
import "./local-quality.css";

type View = { kind: "idle" | "loading" | "error" } | { kind: "sample"; receipt: QualityReceipt; expired: boolean };
const gapCopy: Record<QualityGap, string> = {
  "network-changed": "The interface binding no longer matches enrollment. Review network setup before trying again.",
  "permission-required": "Local interface metadata could not be read with the available permissions. Review the controller's permissions before retrying.",
  unsupported: "This interface is not supported for this local read. Review the enrolled interface in network setup.",
  "source-unavailable": "Local interface metadata or sufficient binding evidence is unavailable. Check this device's connection and retry.",
};

export function LocalConnectionPanel({ mode, load = loadLocalQualityFromWeb }: { mode: "live" | "demo"; load?: LocalQualityLoader }) {
  if (mode === "demo") return (
    <section className="product-card local-quality" aria-labelledby="local-connection-title">
      <p className="eyebrow">Network quality</p><h2 id="local-connection-title">Local connection</h2>
      <p>Synthetic demo: no local interface sample is collected or substituted here. Return to live data to read your enrolled interface.</p>
      <QualityLimits />
    </section>
  );
  return <LiveConnectionPanel load={load} />;
}

function LiveConnectionPanel({ load }: { load: LocalQualityLoader }) {
  const [view, setView] = useState<View>({ kind: "idle" });
  const request = useRef<AbortController | null>(null);
  const requestTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    setView({ kind: "idle" });
    const invalidate = () => {
      request.current?.abort(); request.current = null;
      if (requestTimer.current !== null) clearTimeout(requestTimer.current);
      requestTimer.current = null;
      setView((current) => current.kind === "sample" ? { ...current, expired: true } : current.kind === "loading" ? { kind: "error" } : current);
    };
    // No refresh on focus/resume: require a new user action, and never infer sleep
    // or connectivity failure merely because the document was hidden.
    window.addEventListener("blur", invalidate);
    window.addEventListener("focus", invalidate);
    window.addEventListener("pagehide", invalidate);
    window.addEventListener("pageshow", invalidate);
    document.addEventListener("visibilitychange", invalidate);
    return () => {
      request.current?.abort(); request.current = null;
      if (requestTimer.current !== null) clearTimeout(requestTimer.current);
      requestTimer.current = null;
      window.removeEventListener("blur", invalidate);
      window.removeEventListener("focus", invalidate);
      window.removeEventListener("pagehide", invalidate);
      window.removeEventListener("pageshow", invalidate);
      document.removeEventListener("visibilitychange", invalidate);
    };
  }, [load]);

  useEffect(() => {
    if (view.kind !== "sample" || view.expired || !view.receipt.data.enrolled) return;
    const now = sampleClock();
    const delay = qualityExpired(view.receipt, now) ? 0 : Math.max(0, Math.min(view.receipt.expiresWall - now.wall, view.receipt.expiresMonotonic - now.monotonic));
    const timer = setTimeout(() => setView((current) => current.kind === "sample" ? { ...current, expired: true } : current), delay);
    return () => clearTimeout(timer);
  }, [view]);

  async function readSample() {
    if (request.current !== null) return;
    const controller = new AbortController();
    request.current = controller;
    const start = sampleClock();
    setView({ kind: "loading" });
    requestTimer.current = setTimeout(() => {
      if (request.current !== controller) return;
      controller.abort(); request.current = null; requestTimer.current = null;
      setView({ kind: "error" });
    }, 6000);
    try {
      const data = await load(controller.signal);
      if (!controller.signal.aborted && request.current === controller) {
        const receipt = stampQuality(data, start, sampleClock());
        setView({ kind: "sample", receipt, expired: qualityExpired(receipt, sampleClock()) });
      }
    } catch {
      // Never copy fetch, controller, parser, or arbitrary exception text into UI.
      if (!controller.signal.aborted && request.current === controller) setView({ kind: "error" });
    } finally {
      if (request.current === controller) {
        request.current = null;
        if (requestTimer.current !== null) clearTimeout(requestTimer.current);
        requestTimer.current = null;
      }
    }
  }

  return (
    <section className="product-card local-quality" aria-labelledby="local-connection-title">
      <p className="eyebrow">Network quality</p><h2 id="local-connection-title">Local connection</h2>
      <p>Read the enrolled interface's operating-system metadata on this controller. This does not enable Device Watch or send a network probe.</p>
      <div aria-busy={view.kind === "loading"}>
        {view.kind === "idle" ? <p>No local sample yet. Read one when you need it; there is no background polling.</p> : null}
        {view.kind === "loading" ? <p role="status" aria-label="Local connection sample">Reading local interface metadata…</p> : null}
        {view.kind === "error" ? <p role="alert">Local sample unavailable. Retry this read; when the browser session has expired, reopen the authenticated local Cozy SOC URL. Other evidence views remain available.</p> : null}
        {view.kind === "sample" ? <SampleDetails receipt={view.receipt} expired={view.expired || qualityExpired(view.receipt, sampleClock())} /> : null}
      </div>
      <button type="button" className="primary-action" disabled={view.kind === "loading"} onClick={() => { void readSample(); }}>
        {view.kind === "error" ? "Retry local sample" : view.kind === "sample" ? "Refresh local sample" : "Read local interface"}
      </button>
      <QualityLimits />
    </section>
  );
}

function SampleDetails({ receipt, expired }: { receipt: QualityReceipt; expired: boolean }) {
  const data = receipt.data;
  if (!data.enrolled) return <p>No network is enrolled. Use the Device Watch setup above to authorize a network; enabling monitoring remains a separate choice. No interface was inspected.</p>;
  const check = data.check;
  return (
    <>
      <p role="status" aria-label="Local connection sample">{expired ? "Historical sample — refresh to check again." : "Recent sample — not continuous monitoring."}</p>
      <dl className="local-quality-facts">
        <div><dt>Observed on</dt><dd>This controller · {data.observer.interface_name} (index {data.observer.interface_index})</dd></div>
        <div><dt>{expired ? "Last sampled administrative state" : "Administrative state at sample time"}</dt><dd>{check.administrative_up === undefined ? "Not measured" : check.administrative_up ? "Up" : "Down"}</dd></div>
        <div><dt>Source</dt><dd>Operating-system interface metadata</dd></div>
        <div><dt>Sample completed</dt><dd><time dateTime={check.completed_at}>{formatTime(check.completed_at)}</time></dd></div>
        <div><dt>Freshness deadline</dt><dd><time dateTime={check.fresh_until}>{formatTime(check.fresh_until)}</time></dd></div>
        <div><dt>Assessment confidence</dt><dd>{check.confidence === "limited" ? "Limited to this interface sample" : "Unknown — no interface-state conclusion"}</dd></div>
      </dl>
      {check.gap ? <p>{expired ? "At the last read: " : ""}{gapCopy[check.gap]}</p> : <p>Administrative Up means the interface is enabled in the operating system, not that its physical link or internet connection works.</p>}
      {expired ? <p>This evidence is historical or was invalidated when the page lost focus. Current interface state is unknown until you request another sample.</p> : null}
    </>
  );
}
function QualityLimits() {
  return <p className="local-quality-limits">Gateway, DNS, internet reachability, signal strength, latency, and packet loss have not been measured here. Interface/prefix matching cannot distinguish networks that reuse the same binding. This read is not saved to history and is separate from security findings and monitoring coverage.</p>;
}
function formatTime(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "medium" }).format(new Date(value));
}
