import { useEffect, useRef, useState } from "react";

import { deviceEvidenceExportJSON, type DeviceDetail, type DeviceIdentityEvidence } from "./detail";

export function DeviceDetailPanel({ detail, onBack }: { detail: DeviceDetail; onBack: () => void }) {
  const label = detail.device.user_label ?? "Unlabeled device";
  const heading = useRef<HTMLHeadingElement>(null);
  const [exportJSON, setExportJSON] = useState<string | null>(null);

  useEffect(() => { heading.current?.focus(); }, []);

  const saveExport = () => {
    if (exportJSON === null) return;
    const url = URL.createObjectURL(new Blob([exportJSON], { type: "application/json" }));
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = "cozysoc-device-evidence.json";
    anchor.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 0);
  };

  return (
    <section className="device-detail" aria-labelledby="device-detail-title">
      <button type="button" className="quiet-button device-detail-back" onClick={onBack}>Back to devices</button>
      <div className="product-card device-detail-card">
        <header className="device-detail-header">
          <div><p className="eyebrow">Device evidence</p><h2 id="device-detail-title" ref={heading} tabIndex={-1}>{label}</h2><code>{detail.device.id}</code></div>
          <span className={`presence-pill presence-pill--${detail.device.state}`}>{detail.device.state === "visible" ? "Visible at read" : "Uncertain at read"}</span>
        </header>
        <div className="device-detail-presence">
          <p>Evidence read at <time dateTime={detail.as_of}>{formatTimestamp(detail.as_of)}</time>. Reopen this device from the list for a newer read; this view does not update automatically.</p>
          <strong>{detail.device.state === "visible" ? "Recent positive evidence supported visibility at this read" : "Visibility was uncertain at this read"}</strong>
          <p>{detail.device.state === "visible"
            ? "Device Watch had recently observed this identity from this computer. That is presence evidence, not a trust or safety assessment."
            : "Cozy SOC retained evidence for this device, but no sufficiently recent positive observation supported visibility at this read. That is not proof the device is offline."}</p>
          <dl><div><dt>First seen</dt><dd>{formatTimestamp(detail.device.first_seen)}</dd></div><div><dt>Last seen</dt><dd>{formatTimestamp(detail.device.last_seen)}</dd></div></dl>
        </div>
      </div>

      <div className="product-card device-evidence-card">
        <header className="section-header"><div><p className="eyebrow">Identity evidence</p><h3>Why Cozy SOC associates these identities</h3><p>Current and historical labels are relative to the recorded read time, separately from presence. Confidence describes the association, not whether the device is safe.</p></div></header>
        {detail.truncated ? <div className="inline-notice" role="status">Only the most recent bounded set of retained identity evidence is shown.</div> : null}
        {detail.evidence.length === 0 ? <div className="empty-list-state"><strong>No retained identity evidence is available</strong><p>The device summary remains valid for the retained scope, but its source observations may have aged out under local retention.</p></div> : (
          <ul className="device-evidence-list">
            {detail.evidence.map((item, index) => <EvidenceItem key={`${item.kind}-${item.value}-${item.observed_at}-${index}`} item={item} />)}
          </ul>
        )}
      </div>

      <div className="product-card device-evidence-card">
        <header className="section-header"><div><p className="eyebrow">Resolver history</p><h3>Recent DNS observations</h3><p>These AdGuard Home queries had a client IP matching recent, time-valid identity evidence for this device. The association is inferred; shared addresses, alternate DNS, and missing observations can make it incomplete. This is not network coverage or a safety assessment.</p></div></header>
        {detail.dns_history_truncated ? <div className="inline-notice" role="status">Only the 100 most recently retained resolver observations in this scope were checked.</div> : null}
        {detail.truncated ? <div className="inline-notice" role="status">Identity evidence was limited, so DNS observations were not associated with this device.</div> : null}
        {detail.dns_history.length === 0 ? <div className="empty-list-state"><strong>No associated retained DNS observations</strong><p>This does not mean the device made no DNS requests. Collection is one-shot, scoped, and retained for 24 hours.</p></div> : <ul className="device-evidence-list">{detail.dns_history.map((item, index) => <li key={`${item.observed_at}-${item.client_ip}-${index}`}><div className="evidence-heading"><div><h4><code>{item.name}</code></h4><code>{item.query_type} · {item.client_ip}</code></div><time dateTime={item.observed_at}>{formatTimestamp(item.observed_at)}</time></div><p>{item.filtering === "blocked" ? "Filtered by AdGuard Home" : item.filtering === "not-blocked" ? "Not filtered by AdGuard Home" : "Filtering result unknown"}</p></li>)}</ul>}
      </div>

      <div className="product-card device-evidence-export">
        <h3>Save this evidence snapshot</h3>
        <p>This is the evidence shown for one device at the recorded read time, limited to the most recent 100 identity records. It can include your device label, addresses and source identifiers. Cozy SOC does not upload it; choose a safe place to save it. It is not a backup or complete history.</p>
        <button type="button" className="secondary-action" onClick={() => setExportJSON(deviceEvidenceExportJSON(detail))}>Review JSON before saving</button>
        {exportJSON !== null ? <>
          <pre className="device-evidence-export-preview" aria-label="Device evidence export preview" tabIndex={0}>{exportJSON}</pre>
          <button type="button" className="secondary-action" onClick={saveExport}>Save reviewed JSON</button>
        </> : null}
      </div>
    </section>
  );
}

function EvidenceItem({ item }: { item: DeviceIdentityEvidence }) {
  const confidence = item.link_confidence ?? item.claim_confidence;
  return (
    <li>
      <div className="evidence-heading"><div><span className={`evidence-age evidence-age--${item.current ? "current" : "historical"}`}>{item.current ? "Current identity evidence at read" : "Historical identity evidence at read"}</span><h4>{claimLabel(item.kind)}</h4><code>{item.value}</code></div><span>{formatTimestamp(item.observed_at)}</span></div>
      <dl className="evidence-facts">
        <div><dt>Source</dt><dd>{sourceLabel(item)}</dd></div>
        <div><dt>Association</dt><dd>{item.authority === "user" ? "User-confirmed" : "Inferred"}</dd></div>
        {item.original_device_id ? <div><dt>User correction</dt><dd>Grouped from earlier device <code>{item.original_device_id}</code>. The original observation and association remain unchanged.</dd></div> : null}
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
