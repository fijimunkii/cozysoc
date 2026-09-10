import type { DeviceActivityItem, DeviceActivityList } from "./activity";
import "./activity.css";

export function ActivityPage({ activity }: { activity: DeviceActivityList }) {
  if (!activity.configured) {
    return <section className="product-card empty-product-state" aria-labelledby="activity-title"><p className="eyebrow">Activity</p><h2 id="activity-title">Device activity is not configured</h2><p>Authorize a home network and enable Device Watch to build a local evidence timeline.</p><p className="quiet-note">Cozy SOC does not create departure events from silence. Missing recent evidence appears as uncertainty on Devices.</p></section>;
  }
  return (
    <section className="activity-page" aria-labelledby="activity-title">
      <div className="product-card activity-explainer">
        <p className="eyebrow">Evidence window</p>
        <h2 id="activity-title">Positive device activity</h2>
        <p>This 24-hour view keeps first observations, proven address changes, and each device’s latest positive observation. Quiet periods do not become invented departure events.</p>
        <span className="as-of-label">Through {formatTimestamp(activity.as_of)}</span>
      </div>
      <div className="product-card activity-list-card">
        {activity.truncated ? <div className="inline-notice" role="status">Only the most recent 100 meaningful activity items are shown.</div> : null}
        {activity.items.length === 0 ? <div className="empty-list-state"><strong>No positive device activity in this window</strong><p>This can be normal for a quiet neighbor cache. It does not mean the network is empty, safe, or fully observed.</p></div> : (
          <ol className="activity-list">{activity.items.map((item) => <ActivityItemView key={item.id} item={item} />)}</ol>
        )}
      </div>
    </section>
  );
}

function ActivityItemView({ item }: { item: DeviceActivityItem }) {
  const label = item.user_label ?? "Unlabeled device";
  return <li className={`activity-item activity-item--${item.kind}`}>
    <div className="activity-marker" aria-hidden="true" />
    <div className="activity-content">
      <div className="activity-heading"><div><span className="activity-kind">{kindLabel(item.kind)}</span><h3>{label}</h3><code>{item.device_id}</code></div><time dateTime={item.at}>{formatTimestamp(item.at)}</time></div>
      <p>{eventCopy(item)}</p>
      <dl className="activity-facts"><div><dt>Address</dt><dd><code>{item.address}</code></dd></div><div><dt>MAC</dt><dd><code>{item.hardware_address}</code></dd></div><div><dt>Source</dt><dd>{sourceLabel(item)}</dd></div></dl>
    </div>
  </li>;
}

function kindLabel(kind: DeviceActivityItem["kind"]): string {
  if (kind === "first-observed") return "First observed";
  if (kind === "address-changed") return "Address changed";
  return "Observed recently";
}
function eventCopy(item: DeviceActivityItem): string {
  if (item.kind === "first-observed") return `Cozy SOC first associated this device with ${item.address} from positive neighbor evidence.`;
  if (item.kind === "address-changed") return `Observed ${item.address_family.toUpperCase()} address changed from ${item.previous_address} to ${item.address}.`;
  return `Latest positive neighbor evidence in this window observed ${item.address}.`;
}
function sourceLabel(item: DeviceActivityItem): string {
  return item.source.attribution === "device-watch:arp-cache" ? "ARP neighbor cache" : "IPv6 neighbor cache (NDP)";
}
function formatTimestamp(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}
