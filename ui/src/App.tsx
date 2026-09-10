import { useEffect, useState } from "react";

import "./connection.css";
import { CoveragePanel } from "./coverage/CoveragePanel";
import { loadCoverageFromWeb, type CoverageBundle } from "./coverage/bundle";
import { parseCoverageReport } from "./coverage/parse";
import { demoCoverageRaw } from "./demo/coverage";

const demoCoverage = parseCoverageReport(demoCoverageRaw);

type CoverageView =
  | { mode: "loading" }
  | { mode: "live"; bundle: CoverageBundle }
  | { mode: "unavailable"; message: string }
  | { mode: "demo" };

export interface AppProps {
  loadCoverage?: () => Promise<CoverageBundle>;
}

export function App({ loadCoverage = loadCoverageFromWeb }: AppProps) {
  const [attempt, setAttempt] = useState(0);
  const [view, setView] = useState<CoverageView>({ mode: "loading" });

  useEffect(() => {
    let cancelled = false;
    setView({ mode: "loading" });
    void loadCoverage()
      .then((bundle) => {
        if (!cancelled) setView({ mode: "live", bundle });
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          setView({
            mode: "unavailable",
            message: error instanceof Error ? error.message : "Live coverage is unavailable.",
          });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [attempt, loadCoverage]);

  const retryLive = () => setAttempt((value) => value + 1);

  return (
    <main className="app-shell">
      {view.mode === "loading" ? (
        <div className="connection-banner" role="status" aria-label="Live coverage connection status">
          <strong>Connecting</strong>
          <span>Reading coverage from the local Cozy SOC controller.</span>
        </div>
      ) : null}

      {view.mode === "live" ? (
        <div className="connection-banner connection-banner--live" role="status" aria-label="Live controller data">
          <strong>Live controller data</strong>
          <span>Coverage read locally at {formatTimestamp(view.bundle.as_of)}.</span>
        </div>
      ) : null}

      {view.mode === "unavailable" ? (
        <div className="connection-banner connection-banner--unavailable" role="alert" aria-label="Live monitoring unavailable">
          <div>
            <strong>Live monitoring unavailable</strong>
            <span>{view.message} Synthetic data will never replace live data automatically.</span>
          </div>
          <div className="connection-actions">
            <button type="button" onClick={retryLive}>Retry live connection</button>
            <button type="button" onClick={() => setView({ mode: "demo" })}>Use synthetic demo</button>
          </div>
        </div>
      ) : null}

      {view.mode === "demo" ? (
        <div className="connection-banner connection-banner--demo" role="status" aria-label="Synthetic demo data">
          <div>
            <strong>Synthetic demo</strong>
            <span>This screen is not connected to live monitoring.</span>
          </div>
          <button type="button" onClick={retryLive}>Return to live coverage</button>
        </div>
      ) : null}

      <header className="app-header">
        <div>
          <p className="eyebrow">Cozy SOC</p>
          <h1>Know what is visible. Know what is not.</h1>
          <p className="lede">
            Local-first home-network security that explains its observation boundaries instead of hiding them behind a score.
          </p>
        </div>
      </header>

      {view.mode === "live" && view.bundle.reports.length === 0 ? (
        <section className="empty-state" aria-labelledby="coverage-empty-title">
          <h2 id="coverage-empty-title">No coverage reports yet</h2>
          <p>The controller is reachable, but no capability has reported a coverage contract.</p>
        </section>
      ) : null}

      {view.mode === "live"
        ? view.bundle.reports.map((report) => <CoveragePanel key={report.capability_id} report={report} />)
        : null}

      {view.mode === "demo" ? <CoveragePanel report={demoCoverage} /> : null}
    </main>
  );
}

function formatTimestamp(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "medium",
  }).format(new Date(value));
}
