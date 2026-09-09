import { CoveragePanel } from "./coverage/CoveragePanel";
import { parseCoverageReport } from "./coverage/parse";
import { demoCoverageRaw } from "./demo/coverage";

const demoCoverage = parseCoverageReport(demoCoverageRaw);

export function App() {
  return (
    <main className="app-shell">
      <div className="demo-banner" role="status" aria-label="Synthetic demo data">
        <strong>Synthetic demo</strong>
        <span>This screen is not connected to live monitoring.</span>
      </div>

      <header className="app-header">
        <div>
          <p className="eyebrow">Cozy SOC</p>
          <h1>Know what is visible. Know what is not.</h1>
          <p className="lede">
            Local-first home-network security that explains its observation boundaries instead of hiding them behind a score.
          </p>
        </div>
      </header>

      <CoveragePanel report={demoCoverage} />
    </main>
  );
}
