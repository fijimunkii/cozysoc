import "./devices.css";
import type { DeviceList, DevicePresence, DevicePresenceState } from "./devices";

export function DevicesPage({ devices }: { devices: DeviceList }) {
  if (!devices.configured) {
    return (
      <section className="product-card empty-product-state" aria-labelledby="devices-title">
        <p className="eyebrow">Devices</p>
        <h2 id="devices-title">Device visibility is not configured</h2>
        <p>Cozy SOC has not been authorized to observe a home network yet. It will not choose or probe a network on its own.</p>
        <p className="quiet-note">Network enrollment and Device Watch controls are a separate setup step and are not exposed as browser writes in this read-only slice.</p>
      </section>
    );
  }

  const visible = devices.devices.filter((device) => device.state === "visible").length;
  const uncertain = devices.devices.length - visible;
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

        {devices.truncated ? (
          <div className="inline-notice" role="status">This is a bounded subset of the known device list.</div>
        ) : null}

        {devices.devices.length === 0 ? (
          <div className="empty-list-state">
            <strong>No devices are visible yet</strong>
            <p>A quiet neighbor cache can be valid. Cozy SOC will wait for positive evidence rather than invent an offline or safe state.</p>
          </div>
        ) : (
          <div className="device-table-wrap">
            <table className="device-table">
              <thead>
                <tr><th>Device</th><th>Presence</th><th>Last seen</th><th>First seen</th></tr>
              </thead>
              <tbody>
                {devices.devices.map((device) => <DeviceRow key={device.id} device={device} />)}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </section>
  );
}

function DeviceRow({ device }: { device: DevicePresence }) {
  return (
    <tr>
      <td>
        <strong>{device.user_label ?? "Unlabeled device"}</strong>
        <code>{device.id}</code>
      </td>
      <td><span className={`presence-pill presence-pill--${device.state}`}>{presenceLabel(device.state)}</span></td>
      <td>{formatTimestamp(device.last_seen)}</td>
      <td>{formatTimestamp(device.first_seen)}</td>
    </tr>
  );
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
