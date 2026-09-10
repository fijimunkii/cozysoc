import { useEffect, useMemo, useState } from "react";

import "./connection.css";
import { ActivityPage } from "./activity/ActivityPage";
import { parseDeviceActivity } from "./activity/activity";
import "./app-shell.css";
import { type AppData, loadAppDataFromWeb } from "./app-data";
import { CoveragePanel } from "./coverage/CoveragePanel";
import { parseCoverageReport } from "./coverage/parse";
import { DevicesPage } from "./devices/DevicesPage";
import { loadDeviceDetailFromWeb } from "./devices/detail";
import { parseDeviceList } from "./devices/devices";
import { demoCoverageRaw } from "./demo/coverage";
import { demoActivityRaw } from "./demo/activity";
import { demoDevicesRaw } from "./demo/devices";
import { OverviewPage } from "./OverviewPage";
import { SetupPanel } from "./setup/SetupPanel";
import { createWebSetupClient, parseNetworkList, type DeviceLabelClient, type SetupClient } from "./setup/setup";

type Page = "overview" | "devices" | "activity" | "coverage";
type DataView =
  | { mode: "loading" }
  | { mode: "live"; data: AppData }
  | { mode: "unavailable"; message: string }
  | { mode: "demo"; data: AppData };

const demoData: AppData = {
  coverage: { as_of: "2026-09-09T23:21:00Z", reports: [parseCoverageReport(demoCoverageRaw)] },
  devices: parseDeviceList(demoDevicesRaw),
  activity: parseDeviceActivity(demoActivityRaw),
  networks: parseNetworkList({
    candidates: [],
    candidates_truncated: false,
    enrolled: {
      scope_id: "scope.demo",
      enrolled_at: "2026-09-09T22:30:00Z",
      interface: {
        interface_name: "demo0",
        interface_index: 1,
        prefixes: ["192.0.2.0/24"],
      },
    },
  }),
};

export interface AppProps {
  loadData?: () => Promise<AppData>;
  setupClient?: SetupClient;
  deviceLabelClient?: DeviceLabelClient;
}

const pageCopy: Record<Page, { eyebrow: string; title: string; detail: string }> = {
  overview: { eyebrow: "Overview", title: "Your home at a glance", detail: "Current device evidence and coverage limits, without turning either into a security score." },
  devices: { eyebrow: "Devices", title: "What Cozy SOC has actually seen", detail: "Positive presence evidence, labels, and uncertainty when a Device Watch scope is configured." },
  activity: { eyebrow: "Activity", title: "What changed on your visible network", detail: "A low-noise timeline of positive device evidence and proven identity changes—never inferred departures from silence." },
  coverage: { eyebrow: "Coverage", title: "Know what is visible. Know what is not.", detail: "Observation points, verified scope, expected gaps, and evidence freshness stay explicit." },
};

export function App({ loadData = loadAppDataFromWeb, setupClient, deviceLabelClient }: AppProps) {
  const [attempt, setAttempt] = useState(0);
  const [page, setPage] = useState<Page>("overview");
  const [view, setView] = useState<DataView>({ mode: "loading" });
  const defaultMutationClient = useMemo(() => createWebSetupClient(), []);
  const liveSetupClient = setupClient ?? defaultMutationClient;
  const liveDeviceLabelClient = deviceLabelClient ?? defaultMutationClient;

  useEffect(() => {
    let cancelled = false;
    setView({ mode: "loading" });
    void loadData()
      .then((data) => { if (!cancelled) setView({ mode: "live", data }); })
      .catch((error: unknown) => {
        if (!cancelled) setView({ mode: "unavailable", message: error instanceof Error ? error.message : "Live Cozy SOC data is unavailable." });
      });
    return () => { cancelled = true; };
  }, [attempt, loadData]);

  const retryLive = () => setAttempt((value) => value + 1);
  const activeData = view.mode === "live" || view.mode === "demo" ? view.data : undefined;
  const copy = pageCopy[page];

  return (
    <div className="product-shell">
      <aside className="app-sidebar">
        <div className="brand-lockup"><strong>Cozy SOC</strong><span>Local home security</span></div>
        <nav className="primary-nav" aria-label="Primary">
          <NavButton page="overview" current={page} onNavigate={setPage}>Overview</NavButton>
          <NavButton page="devices" current={page} onNavigate={setPage}>Devices</NavButton>
          <NavButton page="activity" current={page} onNavigate={setPage}>Activity</NavButton>
          <NavButton page="coverage" current={page} onNavigate={setPage}>Coverage</NavButton>
        </nav>
        <p className="sidebar-note">Local-first. Evidence and limitations stay on this machine unless you explicitly add another integration.</p>
      </aside>

      <main className="app-main">
        <ConnectionState view={view} retryLive={retryLive} useDemo={() => setView({ mode: "demo", data: demoData })} />
        <header className="app-header">
          <p className="eyebrow">{copy.eyebrow}</p>
          <h1>{copy.title}</h1>
          <p className="lede">{copy.detail}</p>
        </header>

        {view.mode === "loading" ? <section className="product-card empty-product-state"><h2>Reading local evidence</h2><p>Cozy SOC is connecting to the local controller.</p></section> : null}
        {view.mode === "unavailable" ? <section className="product-card empty-product-state"><h2>Live data is unavailable</h2><p>Retry the local connection or explicitly enter the synthetic demo. Demo data is never substituted automatically.</p></section> : null}

        {activeData && page === "overview" ? (
          <>
            {view.mode === "live" ? <SetupPanel data={activeData} client={liveSetupClient} onChanged={retryLive} /> : null}
            <OverviewPage data={activeData} onNavigate={setPage} />
          </>
        ) : null}
        {activeData && page === "devices" ? (
          view.mode === "live"
            ? <DevicesPage devices={activeData.devices} labelClient={liveDeviceLabelClient} onChanged={retryLive} loadDetail={loadDeviceDetailFromWeb} />
            : <DevicesPage devices={activeData.devices} />
        ) : null}
        {activeData && page === "activity" ? (
          activeData.activity === null
            ? <section className="product-card empty-product-state" aria-labelledby="activity-unavailable-title"><h2 id="activity-unavailable-title">Activity is temporarily unavailable</h2><p>{activeData.activity_error ?? "The local activity projection could not be read. Existing device and coverage evidence remains available."}</p>{view.mode === "live" ? <button type="button" className="primary-action" onClick={retryLive}>Retry activity</button> : null}</section>
            : <ActivityPage activity={activeData.activity} />
        ) : null}
        {activeData && page === "coverage" && activeData.coverage.reports.length === 0 ? (
          <section className="product-card empty-product-state"><h2>No coverage reports yet</h2><p>The controller is reachable, but no capability has reported a coverage contract.</p></section>
        ) : null}
        {activeData && page === "coverage" ? activeData.coverage.reports.map((report) => <CoveragePanel key={report.capability_id} report={report} />) : null}
      </main>
    </div>
  );
}

function NavButton({ page, current, onNavigate, children }: { page: Page; current: Page; onNavigate: (page: Page) => void; children: string }) {
  return <button type="button" aria-current={current === page ? "page" : undefined} onClick={() => onNavigate(page)}>{children}</button>;
}

function ConnectionState({ view, retryLive, useDemo }: { view: DataView; retryLive: () => void; useDemo: () => void }) {
  if (view.mode === "loading") return <div className="connection-banner" role="status" aria-label="Live data connection status"><strong>Connecting</strong><span>Reading local Cozy SOC data.</span></div>;
  if (view.mode === "live") return <div className="connection-banner connection-banner--live" role="status" aria-label="Live controller data"><strong>Live controller data</strong><span>Local evidence read at {formatTimestamp(view.data.coverage.as_of)}.</span></div>;
  if (view.mode === "unavailable") return (
    <div className="connection-banner connection-banner--unavailable" role="alert" aria-label="Live monitoring unavailable">
      <div><strong>Live monitoring unavailable</strong><span>{view.message} Synthetic data will never replace live data automatically.</span></div>
      <div className="connection-actions"><button type="button" onClick={retryLive}>Retry live connection</button><button type="button" onClick={useDemo}>Use synthetic demo</button></div>
    </div>
  );
  return (
    <div className="connection-banner connection-banner--demo" role="status" aria-label="Synthetic demo data">
      <div><strong>Synthetic demo</strong><span>This screen is not connected to live monitoring.</span></div>
      <button type="button" onClick={retryLive}>Return to live data</button>
    </div>
  );
}

function formatTimestamp(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "medium" }).format(new Date(value));
}
