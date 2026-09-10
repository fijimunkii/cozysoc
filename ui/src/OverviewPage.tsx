import type { AppData } from "./app-data";
import { coverageStatePresentation } from "./coverage/presentation";

export function OverviewPage({ data, onNavigate }: { data: AppData; onNavigate: (page: "devices" | "coverage") => void }) {
  const visible = data.devices.devices.filter((device) => device.state === "visible").length;
  const uncertain = data.devices.devices.length - visible;
  const report = data.coverage.reports.find((item) => item.capability_id === "device-watch") ?? data.coverage.reports[0];
  const coverage = report === undefined ? undefined : coverageStatePresentation(report.state);
  const knownLimits = report?.configured
    ? report.observation_points.reduce((total, point) => total + point.gaps.length, 0)
    : undefined;
  const recent = [...data.devices.devices]
    .sort((left, right) => Date.parse(right.last_seen) - Date.parse(left.last_seen))
    .slice(0, 5);

  return (
    <section className="overview-page" aria-labelledby="overview-title">
      <div className="overview-grid">
        <article className="overview-card">
          <span>Device visibility</span>
          <strong>{data.devices.configured ? `${visible} visible now` : "Not configured"}</strong>
          <p>{data.devices.configured ? `${uncertain} uncertain · ${data.devices.devices.length} known` : "No home network has been authorized for Device Watch."}</p>
          <button type="button" onClick={() => onNavigate("devices")}>View devices</button>
        </article>
        <article className="overview-card">
          <span>Coverage</span>
          <strong>{coverage?.label ?? "No report yet"}</strong>
          <p>{coverage?.description ?? "The controller has not returned a coverage report."}</p>
          <button type="button" onClick={() => onNavigate("coverage")}>Review coverage</button>
        </article>
        <article className="overview-card">
          <span>Known limits</span>
          <strong>{knownLimits === undefined ? "Not evaluated" : knownLimits}</strong>
          <p>Explicit observation gaps are shown as limits, never converted into a protection percentage.</p>
          <button type="button" onClick={() => onNavigate("coverage")}>See what is missing</button>
        </article>
      </div>

      <div className="product-card overview-detail">
        <div className="section-heading-row overview-heading-row">
          <div><p className="eyebrow">Recent visibility</p><h2 id="overview-title">Recent device visibility</h2></div>
          {data.devices.configured ? <button type="button" className="quiet-button" onClick={() => onNavigate("devices")}>All devices</button> : null}
        </div>
        {!data.devices.configured ? (
          <p className="overview-empty">Start by enrolling the home network you want Cozy SOC to observe. No network is selected automatically.</p>
        ) : recent.length === 0 ? (
          <p className="overview-empty">No positive device-presence evidence has arrived yet.</p>
        ) : (
          <ul className="recent-device-list">
            {recent.map((device) => (
              <li key={device.id}>
                <div><strong>{device.user_label ?? "Unlabeled device"}</strong><span>{device.id}</span></div>
                <div><span className={`presence-dot presence-dot--${device.state}`} aria-hidden="true" />{device.state === "visible" ? "Visible now" : "Uncertain"}</div>
              </li>
            ))}
          </ul>
        )}
        {report?.next_step ? <aside className="overview-next-step"><span>Recommended next step</span><p>{report.next_step}</p></aside> : null}
      </div>
    </section>
  );
}
