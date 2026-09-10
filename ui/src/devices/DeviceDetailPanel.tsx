import type { DeviceDetail, DeviceIdentityEvidence } from "./detail";

export function DeviceDetailPanel({ detail, onBack }: { detail: DeviceDetail; onBack: () => void }) {
  const label = detail.device.user_label ?? "Unlabeled device";
  return (
    <section className="device-detail" aria-labelledby="device-detail-title">
      <button type="button" className="quiet-button device-detail-back" onClick={onBack}>Back to devices</button>
      <div className="product-card device-detail-card">
        <header className="device-detail-header">
          <div><p className="eyebrow">Device evidence</p><h2 id="device-detail-title">{label}</h2><code>{detail.device.id}</code></div>
          <span className={`presence-pill presence-pill--${detail.device.state}`}>{detail.device.state === "visible" ? "Visible now" : "Uncertain"}</span>
        </header>
        <div className="device-detail-presence">
          <strong>{detail.device.state === "visible" ? "Recent positive evidence supports visibility" : "Recent visibility is uncertain"}</strong>
          <p>{detail.device.state === "visible"
            ? "Device Watch recently observed this identity from this computer. That is presence evidence, not a trust or safety assessment."
            : "Cozy SOC retains evidence for this device, but no sufficiently recent positive observation supports ‘visible now’. That is not proof the device is offline."}</p>
          <dl><div><dt>First seen</dt><dd>{formatTimestamp(detail.device.first_seen)}</dd></div><div><dt>Last seen</dt><dd>{formatTimestamp(detail.device.last_seen)}</dd></div></dl>
        </div>
      </div>

      <div className="product-card device-evidence-card">
        <header className="section-header"><div><p className="eyebrow">Identity evidence</p><h3>Why Cozy SOC associates these identities</h3><p>Current and historical identity evidence are separate from presence. Confidence here describes the association, not whether the device is safe.</p></div></header>
        {detail.truncated ? <div className="inline-notice" role="status">Only the most recent bounded set of retained identity evidence is shown.</div> : null}
        {detail.evidence.length === 0 ? <div className="empty-list-state"><strong>No retained identity evidence is available</strong><p>The device summary remains valid for the retained scope, but its source observations may have aged out under local retention.</p></div> : (
          <ul className="device-evidence-list">
            {detail.evidence.map((item, index) => <EvidenceItem key={`${item.kind}-${item.value}-${item.observed_at}-${index}`} item={item} />)}
          </ul>
        )}
      </div>
    </section>
  );
}

function EvidenceItem({ item }: { item: DeviceIdentityEvidence }) {
  const confidence = item.link_confidence ?? item.claim_confidence;
  return (
    <li>
      <div className="evidence-heading"><div><span className={`evidence-age evidence-age--${item.current ? "current" : "historical"}`}>{item.current ? "Current identity evidence" : "Historical identity evidence"}</span><h4>{claimLabel(item.kind)}</h4><code>{item.value}</code></div><span>{formatTimestamp(item.observed_at)}</span></div>
      <dl className="evidence-facts">
        <div><dt>Source</dt><dd>{sourceLabel(item)}</dd></div>
        <div><dt>Association</dt><dd>{item.authority === "user" ? "User-confirmed" : "Inferred"}</dd></div>
        {confidence !== undefined ? <div><dt>Identity-link confidence</dt><dd>{Math.round(confidence * 100)}% <span>— association only, not device safety</span></dd></div> : null}
        <div><dt>Why linked</dt><dd>{reasonLabel(item.reason)}</dd></div>
        {item.valid_until ? <div><dt>Claim valid until</dt><dd>{formatTimestamp(item.valid_until)}</dd></div> : null}
        {item.link_valid_until ? <div><dt>Association valid until</dt><dd>{formatTimestamp(item.link_valid_until)}</dd></div> : null}
      </dl>
      <details className="evidence-provenance"><summary>Technical provenance</summary><dl><div><dt>Sensor</dt><dd><code>{item.source_sensor_id}</code></dd></div><div><dt>Policy reason</dt><dd><code>{item.reason}</code></dd></div>{item.source ? <><div><dt>Observation</dt><dd><code>{item.source.observation_id}</code></dd></div><div><dt>Stream</dt><dd><code>{item.source.source_stream}</code></dd></div><div><dt>Attribution</dt><dd><code>{item.source.attribution}</code></dd></div></> : <div><dt>Source observation</dt><dd>Raw source metadata has aged out under retention.</dd></div>}</dl></details>
    </li>
  );
}

function claimLabel(kind: DeviceIdentityEvidence["kind"]): string {
  return ({ ipv4: "IPv4 address", ipv6: "IPv6 address", mac: "MAC address", hostname: "Hostname", "service-name": "Service name", "dhcp-client-id": "DHCP client ID", "endpoint-id": "Endpoint ID" })[kind];
}

function sourceLabel(item: DeviceIdentityEvidence): string {
  const attribution = item.source?.attribution;
  if (attribution === "device-watch:arp-cache") return "ARP neighbor cache";
  if (attribution === "device-watch:ndp-cache") return "IPv6 neighbor cache (NDP)";
  return item.source ? `Sensor ${item.source.sensor_id}` : `Sensor ${item.source_sensor_id} (source metadata expired)`;
}

function reasonLabel(reason: string): string {
  if (reason.startsWith("device-watch:new-mac-candidate:")) return "Linked when Device Watch first observed this MAC address.";
  if (reason.startsWith("device-watch:recent-mac-continuity:")) return "Linked using recent continuity of the same MAC address.";
  return "Linked by the local identity reconciliation policy.";
}

function formatTimestamp(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}
