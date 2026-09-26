import { useEffect, useRef, useState } from "react";

import type { DeviceCorrectionClient, DeviceLabelClient, DeviceMergeList } from "../setup/setup";
import { DeviceLoadError } from "./devices";
import { DeviceCorrectionPanel } from "./DeviceCorrectionPanel";
import { DeviceDetailPanel } from "./DeviceDetailPanel";
import type { DeviceDetail } from "./detail";
import { SetupRequestError, validateDeviceLabelInput } from "../setup/setup";
import "./devices.css";
import type { DeviceList, DevicePresence, DevicePresenceState } from "./devices";

type DetailView = { deviceID: string; scopeID: string; state: "loading" | "ready" | "error"; detail?: DeviceDetail; message?: string };

export function DevicesPage({ devices, labelClient, correctionClient, loadMerges, onChanged, onNavigate, loadDetail }: { devices: DeviceList; labelClient?: DeviceLabelClient; correctionClient?: DeviceCorrectionClient; loadMerges?: () => Promise<DeviceMergeList>; onChanged?: () => void; onNavigate?: (page: "overview" | "coverage") => void; loadDetail?: (deviceID: string) => Promise<DeviceDetail> }) {
  const [detailView, setDetailView] = useState<DetailView | null>(null);
  const listHeading = useRef<HTMLHeadingElement>(null);
  const statusHeading = useRef<HTMLHeadingElement>(null);
  const detailRequest = useRef(0);
  const previousDetailState = useRef<"loading" | "ready" | "error" | null>(null);
  const activeDetailView = detailView !== null && devices.scope_id !== undefined && detailView.scopeID === devices.scope_id && devices.devices.some((device) => device.id === detailView.deviceID) ? detailView : null;

  useEffect(() => {
    const state = activeDetailView?.state ?? null;
    if (state === "loading" || state === "error") statusHeading.current?.focus();
    if (state === null && previousDetailState.current !== null) listHeading.current?.focus();
    previousDetailState.current = state;
    if (detailView !== null && activeDetailView === null) {
      detailRequest.current += 1;
      setDetailView(null);
    }
  }, [activeDetailView, detailView]);

  async function openDetail(deviceID: string): Promise<void> {
    const scopeID = devices.scope_id;
    if (loadDetail === undefined || scopeID === undefined) return;
    const requestID = ++detailRequest.current;
    setDetailView({ deviceID, scopeID, state: "loading" });
    try {
      const detail = await loadDetail(deviceID);
      if (requestID !== detailRequest.current) return;
      if (detail.scope_id !== scopeID) throw new DeviceLoadError("Device evidence belongs to a different network scope. Refresh the device list and try again.");
      setDetailView({ deviceID, scopeID, state: "ready", detail });
    } catch (error: unknown) {
      if (requestID !== detailRequest.current) return;
      setDetailView({ deviceID, scopeID, state: "error", message: error instanceof DeviceLoadError ? error.message : "Device evidence could not be loaded." });
    }
  }

  if (activeDetailView?.state === "ready" && activeDetailView.detail !== undefined) return <DeviceDetailPanel detail={activeDetailView.detail} onBack={() => setDetailView(null)} />;
  if (activeDetailView?.state === "loading") return <section className="product-card empty-product-state"><h2 ref={statusHeading} tabIndex={-1}>Reading device evidence</h2><p>Loading retained observations and identity associations from the local controller.</p></section>;
  if (activeDetailView?.state === "error") return <section className="product-card empty-product-state"><h2 ref={statusHeading} tabIndex={-1}>Device evidence is unavailable</h2><p>{activeDetailView.message}</p><div className="device-detail-error-actions"><button type="button" className="secondary-action" onClick={() => void openDetail(activeDetailView.deviceID)}>Retry evidence</button><button type="button" className="quiet-button" onClick={() => setDetailView(null)}>Back to devices</button></div></section>;
  if (!devices.configured) {
    return (
      <section className="product-card empty-product-state" aria-labelledby="devices-title">
        <p className="eyebrow">Devices</p>
        <h2 id="devices-title" ref={listHeading} tabIndex={-1}>Device visibility is not configured</h2>
        <p>Cozy SOC has not been authorized to observe a home network yet. It will not choose or probe a network on its own.</p>
        <p className="quiet-note">Use the guided setup on Overview to authorize a network and enable Device Watch separately.</p>
        {onNavigate ? <button type="button" className="primary-action" onClick={() => onNavigate("overview")}>Open guided setup</button> : null}
      </section>
    );
  }

  const visible = devices.devices.filter((device) => device.state === "visible").length;
  const uncertain = devices.devices.length - visible;
  const canEditLabels = labelClient !== undefined && onChanged !== undefined;
  const canViewEvidence = loadDetail !== undefined;
  return (
    <section className="devices-page" aria-labelledby="devices-title">
      <div className="device-summary-grid" aria-label="Device visibility summary">
        <Summary label="Visible now" value={visible} />
        <Summary label="Uncertain" value={uncertain} />
        <Summary label="Known devices" value={devices.devices.length} />
      </div>

      <div className="product-card device-list-card">
        <header className="section-header">
          <div>
            <p className="eyebrow">Devices</p>
            <h2 id="devices-title" ref={listHeading} tabIndex={-1}>Visible network identities</h2>
            <p>Presence comes from positive Device Watch evidence. Missing evidence ages to uncertain; it is not treated as proof that a device went offline.</p>
          </div>
          <span className="as-of-label">Updated {formatTimestamp(devices.as_of)}</span>
        </header>

        {devices.truncated ? <div className="inline-notice" role="status">This is a bounded subset of the known device list.</div> : null}

        {devices.devices.length === 0 ? (
          <div className="empty-list-state">
            <strong>No devices are visible yet</strong>
            <p>A quiet neighbor cache can be valid. Cozy SOC will wait for positive evidence rather than invent an offline or safe state.</p>
            {onNavigate || onChanged ? <div className="device-empty-actions">
              {onNavigate ? <button type="button" className="secondary-action" onClick={() => onNavigate("coverage")}>Review coverage</button> : null}
              {onChanged ? <button type="button" className="quiet-button" onClick={onChanged}>Refresh device evidence</button> : null}
            </div> : null}
          </div>
        ) : (
          <div className="device-table-wrap">
            <table className="device-table">
              <thead>
                <tr><th>Device</th><th>Presence</th><th>Last seen</th><th>First seen</th>{canViewEvidence ? <th>Evidence</th> : null}{canEditLabels ? <th>Label</th> : null}</tr>
              </thead>
              <tbody>
                {devices.devices.map((device) => <DeviceRow key={device.id} device={device} labelClient={canEditLabels ? labelClient : undefined} onChanged={canEditLabels ? onChanged : undefined} onViewEvidence={canViewEvidence ? () => void openDetail(device.id) : undefined} />)}
              </tbody>
            </table>
          </div>
        )}
      </div>
      {correctionClient !== undefined && loadMerges !== undefined && onChanged !== undefined && devices.scope_id !== undefined ? <DeviceCorrectionPanel key={devices.scope_id} scopeID={devices.scope_id} devices={devices.devices} client={correctionClient} loadMerges={loadMerges} onChanged={onChanged} /> : null}
    </section>
  );
}

function DeviceRow({ device, labelClient, onChanged, onViewEvidence }: { device: DevicePresence; labelClient?: DeviceLabelClient | undefined; onChanged?: (() => void) | undefined; onViewEvidence?: (() => void) | undefined }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(device.user_label ?? "");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const canEdit = labelClient !== undefined && onChanged !== undefined;
  const current = device.user_label ?? "";
  const validation = validateDeviceLabelInput(draft);
  const labelName = device.user_label ?? device.id;
  const bytes = new TextEncoder().encode(draft).length;

  async function applyLabel(label: string): Promise<void> {
    if (!canEdit) return;
    setPending(true);
    setError(null);
    try {
      await labelClient.labelDevice(device.id, label);
      setEditing(false);
      onChanged();
    } catch (caught: unknown) {
      setError(deviceLabelErrorMessage(caught));
    } finally {
      setPending(false);
    }
  }

  return (
    <tr>
      <td>
        {editing ? (
          <form className="device-label-form" onSubmit={(event) => { event.preventDefault(); void applyLabel(draft); }}>
            <label htmlFor={`device-label-${device.id}`}>Device label</label>
            <input
              id={`device-label-${device.id}`}
              aria-label={`Device label for ${labelName}`}
              value={draft}
              disabled={pending}
              aria-invalid={validation === undefined ? undefined : "true"}
              onChange={(event) => setDraft(event.target.value)}
            />
            <div className="device-label-meta"><span>{bytes}/160 UTF-8 bytes</span>{validation ? <span className="device-label-validation">{validation}</span> : null}</div>
            <div className="device-label-form-actions">
              <button type="button" className="quiet-button" disabled={pending} onClick={() => { setDraft(current); setError(null); setEditing(false); }}>Cancel</button>
              <button type="submit" className="primary-action" disabled={pending || validation !== undefined || draft === current}>{pending ? "Saving…" : "Save label"}</button>
            </div>
          </form>
        ) : (
          <><strong>{device.user_label ?? "Unlabeled device"}</strong><code>{device.id}</code></>
        )}
        {error ? <div className="device-label-error" role="alert">{error}</div> : null}
      </td>
      <td><span className={`presence-pill presence-pill--${device.state}`}>{presenceLabel(device.state)}</span></td>
      <td>{formatTimestamp(device.last_seen)}</td>
      <td>{formatTimestamp(device.first_seen)}</td>
      {onViewEvidence ? <td><button type="button" className="quiet-button" onClick={onViewEvidence}>View evidence</button></td> : null}
      {canEdit ? (
        <td className="device-label-actions">
          {!editing ? <button type="button" className="quiet-button" onClick={() => { setDraft(current); setError(null); setEditing(true); }}>{current === "" ? `Name ${device.id}` : `Rename ${current}`}</button> : null}
          {!editing && current !== "" ? <button type="button" className="quiet-button" disabled={pending} onClick={() => void applyLabel("")}>Clear label</button> : null}
        </td>
      ) : null}
    </tr>
  );
}

function deviceLabelErrorMessage(error: unknown): string {
  if (!(error instanceof SetupRequestError)) return "The label change failed. Existing device evidence and label state are unchanged.";
  if (error.code === "not_found") return "This device is no longer available in the current authorized scope. Refresh the device list and try again.";
  if (error.code === "invalid_request") return "That label is not valid. Use a trimmed label no larger than 160 UTF-8 bytes.";
  if (error.code === "web_session_required") return "The local web session expired. Reopen the authenticated Cozy SOC URL and try again.";
  if (error.code === "csrf_required" || error.code === "origin_required") return "The local session could not authorize this label change. Reload the authenticated Cozy SOC page and try again.";
  if (error.code === "controller_unavailable") return "The local controller could not update this label. Retry when the controller is available.";
  return error.message;
}

function Summary({ label, value }: { label: string; value: number }) {
  return <div className="summary-tile"><span>{label}</span><strong>{value}</strong></div>;
}

function presenceLabel(state: DevicePresenceState): string {
  return state === "visible" ? "Visible now" : "Uncertain";
}

function formatTimestamp(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}
