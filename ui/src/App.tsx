import { useEffect, useMemo, useRef, useState } from "react";

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
import { demoCapabilitiesRaw, demoStatusRaw } from "./demo/tools";
import { OverviewPage } from "./OverviewPage";
import { QualityDiagnosisPanel } from "./quality/QualityDiagnosisPanel";
import { loadQualityDiagnosisFromWeb, type QualityDiagnosisLoader } from "./quality/quality-diagnosis";
import { HTTPSHistoryPanel } from "./quality/HTTPSHistoryPanel";
import { loadHTTPSHistoryFromWeb, type HTTPSHistoryLoader } from "./quality/https-history";
import { ResolverHistoryPanel } from "./quality/ResolverHistoryPanel";
import { loadResolverHistoryFromWeb, type ResolverHistoryLoader } from "./quality/resolver-history";
import { GatewayHistoryPanel } from "./quality/GatewayHistoryPanel";
import { GatewayCheckPanel } from "./quality/GatewayCheckPanel";
import { ResolverCheckPanel } from "./quality/ResolverCheckPanel";
import { type ResolverCheckClient } from "./quality/resolver-check";
import { HTTPSCheckPanel } from "./quality/HTTPSCheckPanel";
import { type HTTPSCheckClient } from "./quality/https-check";
import { type GatewayCheckClient } from "./quality/gateway-check";
import { loadGatewayHistoryFromWeb, type GatewayHistoryLoader } from "./quality/gateway-history";
import { LocalConnectionPanel } from "./quality/LocalConnectionPanel";
import { loadLocalQualityFromWeb, type LocalQualityLoader } from "./quality/local-quality";
import { SetupPanel } from "./setup/SetupPanel";
import { createWebSetupClient, loadDeviceMergesFromWeb, loadDeviceSplitsFromWeb, parseNetworkList, type DeviceCorrectionClient, type DeviceLabelClient, type DeviceMergeList, type DeviceSplitClient, type DeviceSplitList, type SetupClient } from "./setup/setup";
import { ToolsPage } from "./tools/ToolsPage";
import { parseToolsSnapshot } from "./tools/tools";

type Page = "overview" | "devices" | "activity" | "coverage" | "tools";
type DataView =
  | { mode: "loading" }
  | { mode: "live"; data: AppData }
  | { mode: "stale"; lastReadAt: number; message: string }
  | { mode: "unavailable"; message: string }
  | { mode: "demo"; data: AppData };

const liveRefreshIntervalMS = 60_000;
const delayedRefreshMS = 90_000;

const demoData: AppData = {
  coverage: { as_of: "2026-09-09T23:21:00Z", reports: [parseCoverageReport(demoCoverageRaw)] },
  devices: parseDeviceList(demoDevicesRaw),
  activity: parseDeviceActivity(demoActivityRaw),
  tools: parseToolsSnapshot(demoStatusRaw, demoCapabilitiesRaw),
  storage: null,
  storage_error: "Live storage information requires a connected local controller.",
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
  deviceCorrectionClient?: DeviceCorrectionClient;
  loadDeviceMerges?: () => Promise<DeviceMergeList>;
  deviceSplitClient?: DeviceSplitClient;
  loadDeviceSplits?: () => Promise<DeviceSplitList>;
  gatewayCheckClient?: GatewayCheckClient;
  resolverCheckClient?: ResolverCheckClient;
  httpsCheckClient?: HTTPSCheckClient;
  loadLocalQuality?: LocalQualityLoader;
  loadGatewayHistory?: GatewayHistoryLoader;
  loadHTTPSHistory?: HTTPSHistoryLoader;
  loadResolverHistory?: ResolverHistoryLoader;
  loadQualityDiagnosis?: QualityDiagnosisLoader;
}

const pageCopy: Record<Page, { eyebrow: string; title: string; detail: string }> = {
  overview: { eyebrow: "Overview", title: "Your home at a glance", detail: "Current device evidence and coverage limits, without turning either into a security score." },
  devices: { eyebrow: "Devices", title: "What Cozy SOC has actually seen", detail: "Positive presence evidence, labels, and uncertainty when a Device Watch scope is configured." },
  activity: { eyebrow: "Activity", title: "What changed on your visible network", detail: "A low-noise timeline of positive device evidence and proven identity changes—never inferred departures from silence." },
  coverage: { eyebrow: "Coverage", title: "Know what is visible. Know what is not.", detail: "Observation points, verified scope, expected gaps, and evidence freshness stay explicit." },
  tools: { eyebrow: "Tools", title: "What Cozy SOC can run", detail: "Capability ownership, operating state, support evidence, and resource limits without turning a running process into a protection claim." },
};

export function App({ loadData = loadAppDataFromWeb, setupClient, deviceLabelClient, deviceCorrectionClient, loadDeviceMerges = loadDeviceMergesFromWeb, deviceSplitClient, loadDeviceSplits = loadDeviceSplitsFromWeb, gatewayCheckClient, resolverCheckClient, httpsCheckClient, loadLocalQuality = loadLocalQualityFromWeb, loadGatewayHistory = loadGatewayHistoryFromWeb, loadResolverHistory = loadResolverHistoryFromWeb, loadHTTPSHistory = loadHTTPSHistoryFromWeb, loadQualityDiagnosis = loadQualityDiagnosisFromWeb }: AppProps) {
  const [attempt, setAttempt] = useState(0);
  const [page, setPage] = useState<Page>("overview");
  const [view, setView] = useState<DataView>({ mode: "loading" });
  const pageHeading = useRef<HTMLHeadingElement>(null);
  const previousPage = useRef<Page>(page);
  const refreshInFlight = useRef(false);
  const lastSuccessfulRead = useRef<number | null>(null);
  const defaultMutationClient = useMemo(() => createWebSetupClient(), []);
  const liveSetupClient = setupClient ?? defaultMutationClient;
  const liveDeviceLabelClient = deviceLabelClient ?? defaultMutationClient;
  const liveDeviceCorrectionClient = deviceCorrectionClient ?? defaultMutationClient;
  const liveDeviceSplitClient = deviceSplitClient ?? defaultMutationClient;
  const liveGatewayCheckClient = gatewayCheckClient ?? defaultMutationClient;
  const liveResolverCheckClient = resolverCheckClient ?? defaultMutationClient;
  const liveHTTPSCheckClient = httpsCheckClient ?? defaultMutationClient;

  useEffect(() => {
    if (previousPage.current !== page) {
      previousPage.current = page;
      pageHeading.current?.focus();
    }
  }, [page]);

  useEffect(() => {
    let cancelled = false;
    refreshInFlight.current = true;
    setView((current) => current.mode === "live" || current.mode === "stale" ? current : { mode: "loading" });
    void Promise.resolve().then(loadData)
      .then((data) => {
        if (!cancelled) {
          lastSuccessfulRead.current = Date.now();
          setView({ mode: "live", data });
        }
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          const message = error instanceof Error ? error.message : "Live Cozy SOC data is unavailable.";
          setView((current) => current.mode === "live"
            ? { mode: "stale", lastReadAt: lastSuccessfulRead.current ?? Date.now(), message }
            : current.mode === "stale"
              ? { ...current, message }
              : { mode: "unavailable", message });
        }
      })
      .finally(() => { if (!cancelled) refreshInFlight.current = false; });
    return () => { cancelled = true; };
  }, [attempt, loadData]);

  useEffect(() => {
    if (view.mode !== "live" && view.mode !== "stale") return;
    const refreshIfIdle = () => {
      if (refreshInFlight.current) return;
      setAttempt((value) => value + 1);
    };
    const timer = window.setInterval(() => {
      if (document.visibilityState !== "visible") return;
      if (lastSuccessfulRead.current !== null && Date.now() - lastSuccessfulRead.current > delayedRefreshMS) {
        setView((current) => current.mode === "live"
          ? { mode: "stale", lastReadAt: lastSuccessfulRead.current ?? Date.now(), message: "This view has not been refreshed recently." }
          : current);
      }
      refreshIfIdle();
    }, liveRefreshIntervalMS);
    const onVisibilityChange = () => {
      if (document.visibilityState !== "visible") return;
      setView((current) => current.mode === "live"
        ? { mode: "stale", lastReadAt: lastSuccessfulRead.current ?? Date.now(), message: "This tab was away. Refreshing local evidence." }
        : current);
      refreshIfIdle();
    };
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [view.mode]);

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
          <NavButton page="tools" current={page} onNavigate={setPage}>Tools</NavButton>
        </nav>
        <p className="sidebar-note">Local-first. Evidence and limitations stay on this machine unless you explicitly add another integration.</p>
      </aside>

      <main className="app-main">
        <ConnectionState view={view} retryLive={retryLive} useDemo={() => setView({ mode: "demo", data: demoData })} />
        <header className="app-header">
          <p className="eyebrow">{copy.eyebrow}</p>
          <h1 ref={pageHeading} tabIndex={-1}>{copy.title}</h1>
          <p className="lede">{copy.detail}</p>
        </header>

        {view.mode === "loading" ? <section className="product-card empty-product-state"><h2>Reading local evidence</h2><p>Cozy SOC is connecting to the local controller.</p></section> : null}
        {view.mode === "unavailable" ? <section className="product-card empty-product-state"><h2>Live data is unavailable</h2><p>Retry the local connection or explicitly enter the synthetic demo. Demo data is never substituted automatically.</p></section> : null}
        {view.mode === "stale" ? <section className="product-card empty-product-state"><h2>Waiting for fresh evidence</h2><p>Current presence and coverage are hidden until the local controller responds. Retry the live read above.</p></section> : null}

        {activeData && page === "overview" ? (
          <>
            {view.mode === "live" ? <SetupPanel data={activeData} client={liveSetupClient} onChanged={retryLive} onReviewCoverage={() => setPage("coverage")} /> : null}
            <OverviewPage data={activeData} onNavigate={setPage} />
            <LocalConnectionPanel key={view.mode} mode={view.mode === "live" ? "live" : "demo"} load={loadLocalQuality} />
            <GatewayCheckPanel key={`gateway-check-${view.mode}`} mode={view.mode === "live" ? "live" : "demo"} client={liveGatewayCheckClient} />
            <ResolverCheckPanel key={`resolver-check-${view.mode}`} mode={view.mode === "live" ? "live" : "demo"} client={liveResolverCheckClient} />
            <HTTPSCheckPanel key={`https-check-${view.mode}`} mode={view.mode === "live" ? "live" : "demo"} client={liveHTTPSCheckClient} />
            <QualityDiagnosisPanel key={`diagnosis-${view.mode}`} mode={view.mode === "live" ? "live" : "demo"} load={loadQualityDiagnosis} />
            <HTTPSHistoryPanel key={`https-history-${view.mode}`} mode={view.mode === "live" ? "live" : "demo"} load={loadHTTPSHistory} />
            <ResolverHistoryPanel key={`resolver-history-${view.mode}`} mode={view.mode === "live" ? "live" : "demo"} load={loadResolverHistory} />
            <GatewayHistoryPanel key={`history-${view.mode}`} mode={view.mode === "live" ? "live" : "demo"} load={loadGatewayHistory} />
          </>
        ) : null}
        {activeData && page === "devices" ? (
          activeData.devices === null
            ? <section className="product-card empty-product-state device-read-unavailable" aria-labelledby="devices-unavailable-title"><h2 id="devices-unavailable-title">Device evidence is temporarily unavailable</h2><p>Current presence is unknown. Coverage and other local evidence can still be read.</p>{view.mode === "live" ? <button type="button" className="primary-action" onClick={retryLive}>Retry device evidence</button> : null}</section>
            : view.mode === "live"
            ? <DevicesPage devices={activeData.devices} labelClient={liveDeviceLabelClient} correctionClient={liveDeviceCorrectionClient} loadMerges={loadDeviceMerges} splitClient={liveDeviceSplitClient} loadSplits={loadDeviceSplits} onChanged={retryLive} onNavigate={setPage} loadDetail={loadDeviceDetailFromWeb} />
            : <DevicesPage devices={activeData.devices} />
        ) : null}
        {activeData && page === "activity" ? (
          activeData.activity === null
            ? <section className="product-card empty-product-state" aria-labelledby="activity-unavailable-title"><h2 id="activity-unavailable-title">Activity is temporarily unavailable</h2><p>{activeData.activity_error ?? "The local activity projection could not be read. Existing device and coverage evidence remains available."}</p>{view.mode === "live" ? <button type="button" className="primary-action" onClick={retryLive}>Retry activity</button> : null}</section>
            : <ActivityPage activity={activeData.activity} mode={view.mode === "live" ? "live" : "demo"} />
        ) : null}
        {activeData && page === "coverage" && activeData.coverage.reports.length === 0 ? (
          <section className="product-card empty-product-state"><h2>No coverage reports yet</h2><p>The controller is reachable, but no capability has reported a coverage contract.</p></section>
        ) : null}
        {activeData && page === "coverage" ? activeData.coverage.reports.map((report) => <CoveragePanel key={report.capability_id} report={report} />) : null}
        {activeData && page === "tools" ? (
          activeData.tools === null
            ? <section className="product-card empty-product-state" aria-labelledby="tools-unavailable-title"><h2 id="tools-unavailable-title">Tool information is temporarily unavailable</h2><p>{activeData.tools_error ?? "The local capability catalog could not be read. Device, activity, and coverage evidence remains available."}</p>{view.mode === "live" ? <button type="button" className="primary-action" onClick={retryLive}>Retry tools</button> : null}</section>
            : <ToolsPage tools={activeData.tools} storage={activeData.storage} storageError={activeData.storage_error} mode={view.mode === "live" ? "live" : "demo"} onNavigate={(target) => setPage(target)} />
        ) : null}
      </main>
    </div>
  );
}

function NavButton({ page, current, onNavigate, children }: { page: Page; current: Page; onNavigate: (page: Page) => void; children: string }) {
  return <button type="button" aria-current={current === page ? "page" : undefined} onClick={() => onNavigate(page)}>{children}</button>;
}

function ConnectionState({ view, retryLive, useDemo }: { view: DataView; retryLive: () => void; useDemo: () => void }) {
  if (view.mode === "loading") return <div className="connection-banner" role="status" aria-label="Live data connection status"><strong>Connecting</strong><span>Reading local Cozy SOC data.</span></div>;
  if (view.mode === "live") return <div className="connection-banner connection-banner--live" role="status" aria-label="Live controller data"><div><strong>Live controller data</strong><span>Local evidence read at {formatTimestamp(view.data.coverage.as_of)}.</span></div><button type="button" onClick={retryLive}>Refresh evidence</button></div>;
  if (view.mode === "stale") return <div className="connection-banner connection-banner--stale" role="alert" aria-label="Live evidence out of date"><div><strong>Live evidence may be out of date</strong><span>{view.message} Last successful read: {formatTimestamp(view.lastReadAt)}.</span></div><button type="button" onClick={retryLive}>Retry live read</button></div>;
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

function formatTimestamp(value: string | number): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "medium" }).format(new Date(value));
}
