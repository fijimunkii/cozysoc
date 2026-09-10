import { useState } from "react";

import type { DeviceLabelClient } from "../setup/setup";
import { DeviceLoadError } from "./devices";
import { DeviceDetailPanel } from "./DeviceDetailPanel";
import type { DeviceDetail } from "./detail";
import { SetupRequestError, validateDeviceLabelInput } from "../setup/setup";
import "./devices.css";
import type { DeviceList, DevicePresence, DevicePresenceState } from "./devices";

export function DevicesPage({ devices, labelClient, onChanged, loadDetail }: { devices: DeviceList; labelClient?: DeviceLabelClient; onChanged?: () => void; loadDetail?: (deviceID: string) => Promise<DeviceDetail> }) {
  const [detailView, setDetailView] = useState<{ deviceID: string; state: "loading" | "ready" | "error"; detail?: DeviceDetail; message?: string } | null>(null);

  async function openDetail(deviceID: string): Promise<void> {
    if (loadDetail === undefined) return;
    setDetailView({ deviceID, state: "loading" });
    try {
      const detail = await loadDetail(deviceID);
      setDetailView({ deviceID, state: "ready", detail });
    } catch (error: unknown) {
      setDetailView({ deviceID, state: "error", message: error instanceof DeviceLoadError ? error.message : "Device evidence could not be loaded." });
    }
  }

  if (detailView?.state === "ready" && detailView.detail !== undefined) return <DeviceDetailPanel detail={detailView.detail} onBack={() => setDetailView(null)} />;
  if (detailView?.state === "loading") return <section className="product-card empty-product-state"><h2>Reading device evidence</h2><p>Loading retained observations and identity associations from the local controller.</p></section>;
  if (detailView?.state === "error") return <section className="product-card empty-product-state"><h2>Device evidence is unavailable</h2><p>{detailView.message}</p><div className="device-detail-error-actions"><button type="button" className="secondary-action" onClick={() => void openDetail(detailView.deviceID)}>Retry evidence</button><button type="button" className="quiet-button" onClick={() => setDetailView(null)}>Back to devices</button></div></section>;
  if (!devices.configured) {
    return (
      <section className="product-card empty-product-state" aria-labelledby="devices-title">
        <p className="eyebrow">Devices</p>
        <h2 id="devices-title">Device visibility is not configured</h2>
        <p>Cozy SOC has not been authorized to observe a home network yet. It will not choose or probe a network on its own.</p>
        <p className="quiet-note">Use the guided setup on Overview to authorize a network and enable Device Watch separately.</p>
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
            <h2 id="devices-title">Visible network identities</h2>
            <p>Presence comes from positive Device Watch evidence. Missing evidence ages to uncertain; it is not treated as proof that a device went offline.</p>
          </div>
          <span className="as-of-label">Updated {formatTimestamp(devices.as_of)}</span>
        </header>

        {devices.truncated ? <div className="inline-notice" role="status">This is a bounded subset of the known device list.</div> : null}

        {devices.devices.length === 0 ? (
          <div className="empty-list-state">
            <strong>No devices are visible yet</strong>
            <p>A quiet neighbor cache can be valid. Cozy SOC will wait for positive evidence rather than invent an offline or safe state.</p>
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
