import { useEffect, useRef, useState } from "react";

import { SetupRequestError, type DeviceSplitClient, type DeviceSplitList } from "../setup/setup";
import type { DeviceDetail, DeviceIdentityEvidence } from "./detail";
import type { DevicePresence } from "./devices";

type Review = { action: "split"; sourceID: string; observationID: string; targetID: string; evidence: DeviceIdentityEvidence[] } | { action: "undo"; sourceID: string; observationID: string; targetID: string };
type SplitsState = { status: "loading" | "ready" | "error"; list?: DeviceSplitList; message?: string };
type DetailState = { status: "idle" | "loading" | "ready" | "error"; detail?: DeviceDetail; message?: string };

export function DeviceSplitPanel({ scopeID, devices, client, loadSplits, loadDetail, onChanged }: {
  scopeID: string;
  devices: DevicePresence[];
  client: DeviceSplitClient;
  loadSplits: () => Promise<DeviceSplitList>;
  loadDetail: (deviceID: string) => Promise<DeviceDetail>;
  onChanged: () => void;
}) {
  const [splits, setSplits] = useState<SplitsState>({ status: "loading" });
  const [sourceID, setSourceID] = useState("");
  const [detail, setDetail] = useState<DetailState>({ status: "idle" });
  const [observationID, setObservationID] = useState("");
  const [targetID, setTargetID] = useState("");
  const [review, setReview] = useState<Review | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loadVersion, setLoadVersion] = useState(0);
  const heading = useRef<HTMLHeadingElement>(null);
  const reviewHeading = useRef<HTMLHeadingElement>(null);

  useEffect(() => { if (review !== null) reviewHeading.current?.focus(); }, [review]);
  useEffect(() => {
    let active = true;
    setSplits({ status: "loading" });
    void loadSplits().then((list) => {
      if (!active) return;
      if (!list.configured || list.scope_id !== scopeID) throw new SetupRequestError("invalid_response", "Device splits belong to a different network. Refresh and try again.");
      setSplits({ status: "ready", list });
    }).catch((cause: unknown) => { if (active) setSplits({ status: "error", message: splitError(cause) }); });
    return () => { active = false; };
  }, [scopeID, loadSplits, loadVersion]);

  useEffect(() => {
    if (sourceID === "") { setDetail({ status: "idle" }); return; }
    let active = true;
    setDetail({ status: "loading" });
    void loadDetail(sourceID).then((value) => {
      if (!active) return;
      if (value.scope_id !== scopeID || value.device.id !== sourceID) throw new SetupRequestError("invalid_response", "Device evidence changed networks. Refresh and try again.");
      setDetail({ status: "ready", detail: value });
    }).catch((cause: unknown) => { if (active) setDetail({ status: "error", message: splitError(cause) }); });
    return () => { active = false; };
  }, [scopeID, sourceID, loadDetail, loadVersion]);

  const observations = new Map<string, DeviceIdentityEvidence[]>();
  for (const item of detail.detail?.evidence ?? []) {
    if (item.source?.kind !== "device-neighbor-seen" || item.authority !== "inferred" || item.original_device_id !== undefined) continue;
    const id = item.source.observation_id;
    observations.set(id, [...(observations.get(id) ?? []), item]);
  }
  const selected = observations.get(observationID);
  const canReview = splits.status === "ready" && detail.status === "ready" && selected !== undefined && selected.length > 0 && sourceID !== targetID;

  async function apply(): Promise<void> {
    if (review === null || pending || splits.status !== "ready") return;
    if (review.action === "split" && (review.sourceID !== sourceID || review.observationID !== observationID || review.targetID !== targetID || !observations.has(review.observationID))) {
      setError("The selected evidence changed. Refresh and review this observation again.");
      return;
    }
    if (review.action === "undo" && !splits.list?.splits.some((item) => item.observation_id === review.observationID && item.source_device_id === review.sourceID && item.target_device_id === review.targetID)) {
      setError("This split is no longer active. Refresh corrections and review again.");
      return;
    }
    setPending(true);
    setError(null);
    try {
      if (review.action === "split") await client.splitObservation(review.sourceID, review.observationID, review.targetID || undefined);
      else await client.unsplitObservation(review.observationID);
      setReview(null);
      setSourceID("");
      setObservationID("");
      setTargetID("");
      heading.current?.focus();
      onChanged();
      setLoadVersion((version) => version + 1);
    } catch (cause: unknown) {
      setError(splitError(cause));
    } finally {
      setPending(false);
    }
  }

  return (
    <section className="product-card device-correction-card" aria-labelledby="device-split-title">
      <header className="section-header"><div><p className="eyebrow">Identity corrections</p><h2 id="device-split-title" ref={heading} tabIndex={-1}>Separate an observation</h2><p>If one neighbor observation was inferred into the wrong device, review it here. Its retained claims move together in the current view; the original evidence stays intact. Future use of the same MAC can remain ambiguous.</p></div></header>
      {splits.status === "loading" ? <p role="status">Reading current splits…</p> : null}
      {splits.status === "error" ? <div role="alert"><p>{splits.message}</p><button type="button" className="quiet-button" onClick={() => setLoadVersion((version) => version + 1)}>Retry splits</button></div> : null}
      {splits.status === "ready" ? <div className="device-correction-content">
        <div className="device-correction-fields">
          <label>Device with a wrong observation<select value={sourceID} disabled={pending} onChange={(event) => { setSourceID(event.target.value); setObservationID(""); setReview(null); }}><option value="">Select a device</option>{devices.map((device) => <option key={device.id} value={device.id}>{device.user_label ?? "Unlabeled device"} · {device.id}</option>)}</select></label>
          {detail.status === "loading" ? <p role="status">Reading this device’s retained observations…</p> : null}
          {detail.status === "error" ? <p role="alert">{detail.message}</p> : null}
          {detail.status === "ready" ? <><label>Observation to separate<select value={observationID} disabled={pending} onChange={(event) => { setObservationID(event.target.value); setReview(null); }}><option value="">Select an observation</option>{[...observations].map(([id, items]) => <option key={id} value={id}>{new Date(items[0]!.observed_at).toLocaleString()} · {items.map((item) => item.value).join(" / ")} · {id}</option>)}</select></label><label>Where it belongs<select value={targetID} disabled={pending} onChange={(event) => { setTargetID(event.target.value); setReview(null); }}><option value="">Create a separate device</option>{devices.filter((device) => device.id !== sourceID).map((device) => <option key={device.id} value={device.id}>{device.user_label ?? "Unlabeled device"} · {device.id}</option>)}</select></label>{detail.detail?.truncated ? <p className="quiet-note">Only the recent bounded evidence page can be selected. Older observations are not offered here.</p> : null}</> : null}
        </div>
        <div className="device-correction-actions"><button type="button" className="secondary-action" disabled={!canReview || pending} onClick={() => { if (selected) { setError(null); setReview({ action: "split", sourceID, observationID, targetID, evidence: selected }); } }}>Review split</button><button type="button" className="quiet-button" disabled={pending} onClick={() => setLoadVersion((version) => version + 1)}>Refresh splits</button></div>
        <div className="device-correction-active"><h3>Current splits</h3>{splits.list?.splits.length === 0 ? <p>No observations are currently separated.</p> : <ul>{splits.list?.splits.map((item) => <li key={item.observation_id}><span>Observation <code>{item.observation_id}</code> from <code>{item.source_device_id}</code> appears under <code>{item.target_device_id}</code></span><button type="button" className="quiet-button" disabled={pending} onClick={() => setReview({ action: "undo", sourceID: item.source_device_id, observationID: item.observation_id, targetID: item.target_device_id })}>Review split undo</button></li>)}</ul>}</div>
        {review ? <div className="device-correction-review" role="group" aria-label="Review observation split"><h3 ref={reviewHeading} tabIndex={-1}>{review.action === "split" ? "Confirm observation split" : "Confirm split undo"}</h3><p>{review.action === "split" ? <>Move observation <code>{review.observationID}</code> from <code>{review.sourceID}</code> {review.targetID ? <>to <code>{review.targetID}</code></> : "to a new device"}. The original inferred links remain available as provenance.</> : <>Restore observation <code>{review.observationID}</code> to <code>{review.sourceID}</code> from <code>{review.targetID}</code>.</>}</p>{review.action === "split" ? <ul>{review.evidence.map((item) => <li key={`${item.kind}-${item.value}`}>{item.kind}: <code>{item.value}</code></li>)}</ul> : null}<div><button type="button" className="quiet-button" disabled={pending} onClick={() => { setReview(null); heading.current?.focus(); }}>Cancel</button><button type="button" className="primary-action" disabled={pending || review.action === "split" && !canReview} onClick={() => void apply()}>{pending ? "Applying…" : review.action === "split" ? "Confirm split" : "Confirm split undo"}</button></div></div> : null}
        {error ? <p className="device-correction-error" role="alert">{error}</p> : null}
      </div> : null}
    </section>
  );
}

function splitError(cause: unknown): string {
  if (!(cause instanceof SetupRequestError)) return "Device splits could not be loaded or changed. Refresh and try again.";
  if (cause.code === "conflict") return "This split conflicts with a current identity correction. Refresh and review again.";
  if (cause.code === "not_found") return "This observation is no longer available in the authorized network. Refresh and review again.";
  if (cause.code === "web_session_required" || cause.code === "csrf_required" || cause.code === "origin_required") return "The local web session expired. Reopen the authenticated Cozy SOC URL and try again.";
  if (cause.code === "controller_unavailable") return "The local controller is unavailable. Retry when it reconnects.";
  return cause.message;
}
