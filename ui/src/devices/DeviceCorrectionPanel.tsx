import { useEffect, useRef, useState } from "react";

import { SetupRequestError, type DeviceCorrectionClient, type DeviceMergeList } from "../setup/setup";
import type { DevicePresence } from "./devices";

type Review = { action: "merge" | "undo"; sourceID: string; targetID: string };
type MergesState = { status: "loading" | "ready" | "error"; list?: DeviceMergeList; message?: string };

export function DeviceCorrectionPanel({ scopeID, devices, client, loadMerges, onChanged }: {
  scopeID: string;
  devices: DevicePresence[];
  client: DeviceCorrectionClient;
  loadMerges: () => Promise<DeviceMergeList>;
  onChanged: () => void;
}) {
  const [merges, setMerges] = useState<MergesState>({ status: "loading" });
  const [sourceID, setSourceID] = useState("");
  const [targetID, setTargetID] = useState("");
  const [review, setReview] = useState<Review | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loadVersion, setLoadVersion] = useState(0);
  const heading = useRef<HTMLHeadingElement>(null);
  const reviewHeading = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    if (review !== null) reviewHeading.current?.focus();
  }, [review]);

  useEffect(() => {
    let active = true;
    setMerges({ status: "loading" });
    void loadMerges().then((list) => {
      if (!active) return;
      if (!list.configured || list.scope_id !== scopeID) throw new SetupRequestError("invalid_response", "Device corrections belong to a different network. Refresh devices and try again.");
      setMerges({ status: "ready", list });
    }).catch((cause: unknown) => {
      if (active) setMerges({ status: "error", message: correctionError(cause) });
    });
    return () => { active = false; };
  }, [scopeID, loadMerges, loadVersion]);

  const list = merges.list;
  const available = devices.filter((device) => !list?.merges.some((item) => item.target_device_id === device.id));
  const selectedSource = devices.find((device) => device.id === sourceID);
  const selectedTarget = devices.find((device) => device.id === targetID);
  const canReview = merges.status === "ready" && selectedSource !== undefined && selectedTarget !== undefined && sourceID !== targetID && available.some((device) => device.id === sourceID);

  async function apply(): Promise<void> {
    if (review === null || pending || merges.status !== "ready" || list === undefined) return;
    if (review.action === "merge" && (!devices.some((device) => device.id === review.sourceID) || !devices.some((device) => device.id === review.targetID))) {
      setError("The reviewed devices changed. Refresh the device list and review again.");
      return;
    }
    if (review.action === "undo" && !list.merges.some((item) => item.source_device_id === review.sourceID && item.target_device_id === review.targetID)) {
      setError("This merge is no longer active. Refresh corrections and review again.");
      return;
    }
    setPending(true);
    setError(null);
    try {
      if (review.action === "merge") await client.mergeDevices(review.sourceID, review.targetID);
      else await client.unmergeDevice(review.sourceID);
      setReview(null);
      setSourceID("");
      setTargetID("");
      heading.current?.focus();
      onChanged();
      setLoadVersion((version) => version + 1);
    } catch (cause: unknown) {
      setError(correctionError(cause));
    } finally {
      setPending(false);
    }
  }

  return (
    <section className="product-card device-correction-card" aria-labelledby="device-correction-title">
      <header className="section-header"><div><p className="eyebrow">Identity corrections</p><h2 id="device-correction-title" ref={heading} tabIndex={-1}>Group duplicate devices</h2><p>If two records describe one device, review both IDs before grouping them. This changes current device views and future matching; original observations and associations remain intact. You can undo a merge.</p></div></header>
      {merges.status === "loading" ? <p className="device-correction-status" role="status">Reading current corrections…</p> : null}
      {merges.status === "error" ? <div className="device-correction-status" role="alert"><p>{merges.message}</p><button type="button" className="quiet-button" onClick={() => setLoadVersion((version) => version + 1)}>Retry corrections</button></div> : null}
      {merges.status === "ready" ? (
        <div className="device-correction-content">
          <div className="device-correction-fields">
            <label>Earlier device to group<select value={sourceID} disabled={pending} onChange={(event) => { setSourceID(event.target.value); setReview(null); }}><option value="">Select a device</option>{available.map((device) => <option key={device.id} value={device.id}>{device.user_label ?? "Unlabeled device"} · {device.id}</option>)}</select></label>
            <label>Device to keep<select value={targetID} disabled={pending} onChange={(event) => { setTargetID(event.target.value); setReview(null); }}><option value="">Select a device</option>{devices.map((device) => <option key={device.id} value={device.id}>{device.user_label ?? "Unlabeled device"} · {device.id}</option>)}</select></label>
          </div>
          <div className="device-correction-actions"><button type="button" className="secondary-action" disabled={!canReview || pending} onClick={() => { setError(null); setReview({ action: "merge", sourceID, targetID }); }}>Review merge</button><button type="button" className="quiet-button" disabled={pending} onClick={() => setLoadVersion((version) => version + 1)}>Refresh corrections</button></div>
          <div className="device-correction-active"><h3>Current merges</h3>{list?.merges.length === 0 ? <p>No device identities are currently grouped.</p> : <ul>{list?.merges.map((item) => <li key={item.source_device_id}><span><code>{item.source_device_id}</code> grouped into <code>{item.target_device_id}</code></span><button type="button" className="quiet-button" disabled={pending} onClick={() => setReview({ action: "undo", sourceID: item.source_device_id, targetID: item.target_device_id })}>Review undo</button></li>)}</ul>}</div>
          {review ? <div className="device-correction-review" role="group" aria-label="Review device correction"><h3 ref={reviewHeading} tabIndex={-1}>{review.action === "merge" ? "Confirm identity merge" : "Confirm identity undo"}</h3><p>{review.action === "merge" ? <>Group <code>{review.sourceID}</code> into <code>{review.targetID}</code>. The first ID disappears from the current list; its evidence stays available under the second ID.</> : <>Restore <code>{review.sourceID}</code> as a separate device from <code>{review.targetID}</code>. Original evidence determines each restored view.</>}</p><div><button type="button" className="quiet-button" disabled={pending} onClick={() => { setReview(null); heading.current?.focus(); }}>Cancel</button><button type="button" className="primary-action" disabled={pending || (review.action === "merge" && !canReview) || (review.action === "undo" && !list?.merges.some((item) => item.source_device_id === review.sourceID && item.target_device_id === review.targetID))} onClick={() => void apply()}>{pending ? "Applying…" : review.action === "merge" ? "Confirm merge" : "Confirm undo"}</button></div></div> : null}
          {error ? <p className="device-correction-error" role="alert">{error}</p> : null}
        </div>
      ) : null}
    </section>
  );
}

function correctionError(cause: unknown): string {
  if (!(cause instanceof SetupRequestError)) return "Device corrections could not be loaded or changed. Refresh and try again.";
  if (cause.code === "conflict") return "This correction conflicts with a current merge. Refresh the device list and review again.";
  if (cause.code === "not_found") return "One of these devices is no longer available in the authorized network. Refresh and review again.";
  if (cause.code === "web_session_required" || cause.code === "csrf_required" || cause.code === "origin_required") return "The local web session expired. Reopen the authenticated Cozy SOC URL and try again.";
  if (cause.code === "controller_unavailable") return "The local controller is unavailable. Retry when it reconnects.";
  return cause.message;
}
