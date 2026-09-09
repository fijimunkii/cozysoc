import type { CoverageDimension, CoverageObservationPoint, CoverageReport, CoverageSourceState } from "./types";
import {
  coverageCadencePresentation,
  coverageCapabilityTitle,
  coverageDimensionPresentation,
  coverageEvidencePresentation,
  coveragePointTitle,
  coverageStatePresentation,
} from "./presentation";

export interface CoveragePanelProps {
  report: CoverageReport;
}

export function CoveragePanel({ report }: CoveragePanelProps) {
  const state = coverageStatePresentation(report.state);

  return (
    <section className="coverage-panel" aria-labelledby="coverage-title">
      <header className="coverage-header">
        <div>
          <p className="eyebrow">Coverage</p>
          <h2 id="coverage-title">What Cozy SOC can see</h2>
          <p className="coverage-intro">
            {coverageCapabilityTitle(report.capability_id)} is reported from current evidence. Known observation gaps stay visible even when a source is working.
          </p>
        </div>
        <div className={`status-pill status-pill--${report.state}`} role="status" aria-label={`Coverage status: ${state.label}`}>
          {state.label}
        </div>
      </header>

      <p className="state-description">{state.description}</p>

      {!report.configured ? (
        <NextStep text={report.next_step} />
      ) : (
        <div className="observation-points">
          {report.observation_points.map((point) => (
            <ObservationPointCard key={point.id} point={point} />
          ))}
        </div>
      )}
    </section>
  );
}

function ObservationPointCard({ point }: { point: CoverageObservationPoint }) {
  const state = coverageStatePresentation(point.state);
  const domID = `coverage-${point.id.replace(/[^a-zA-Z0-9_-]/g, "-")}`;

  return (
    <article className="observation-card" aria-labelledby={`${domID}-title`}>
      <header className="observation-card__header">
        <div>
          <p className="observation-label">Observation point</p>
          <h3 id={`${domID}-title`}>{coveragePointTitle(point)}</h3>
        </div>
        <span className={`status-pill status-pill--${point.state}`}>{state.label}</span>
      </header>

      <p className="observation-summary">{state.description}</p>
      <div className="scope-grid">
        <ScopeGroup title="Configured scope" dimensions={point.scope.configured} empty="No configured scope dimensions were reported." />
        <ScopeGroup title="Verified now" dimensions={point.scope.verified} empty="Nothing in the configured scope is currently verified." />
        <ScopeGroup
          title="Expected, not verified"
          dimensions={point.scope.expected_unverified}
          empty="No configured dimensions are currently marked unverified."
        />
      </div>

      <div className="evidence-strip">
        <p>{coverageEvidencePresentation(point)}</p>
        <p>{coverageCadencePresentation(point.cadence)}.</p>
      </div>

      {point.directions.length > 0 && (
        <div className="direction-block">
          <h4>Observed directions</h4>
          <ul className="chip-list" aria-label="Observed directions">
            {point.directions.map((direction) => (
              <li key={direction} className="chip">
                {humanize(direction)}
              </li>
            ))}
          </ul>
        </div>
      )}

      <section className="gap-section" aria-labelledby={`${domID}-gaps`}>
        <div className="section-heading-row">
          <h4 id={`${domID}-gaps`}>Known limits</h4>
          <span>{point.gaps.length}</span>
        </div>
        {point.gaps.length === 0 ? (
          <p className="empty-copy">No explicit gaps were reported in this response.</p>
        ) : (
          <ul className="gap-list">
            {point.gaps.map((gap) => (
              <li key={gap.id} className="gap-card">
                <strong>{gap.summary}</strong>
                <p>{gap.detail}</p>
                {(gap.dimensions.length > 0 || gap.directions.length > 0) && (
                  <p className="gap-affects">
                    Affects: {[...gap.dimensions.map((dimension) => coverageDimensionPresentation(dimension).value), ...gap.directions.map(humanize)].join(", ")}
                  </p>
                )}
                <p className="gap-next-step">Next step: {gap.next_step}</p>
              </li>
            ))}
          </ul>
        )}
      </section>

      <details className="technical-details">
        <summary>Technical source status</summary>
        {point.sources.length === 0 ? (
          <p className="empty-copy">No source details were reported.</p>
        ) : (
          <ul className="source-list">
            {point.sources.map((source) => (
              <li key={source.id}>
                <div>
                  <strong>{humanize(source.kind)}</strong>
                  <span>{source.id}</span>
                </div>
                <span>{sourceStateLabel(source.state)}</span>
              </li>
            ))}
          </ul>
        )}
      </details>

      <NextStep text={point.next_step} />
    </article>
  );
}

function ScopeGroup({ title, dimensions, empty }: { title: string; dimensions: CoverageDimension[]; empty: string }) {
  return (
    <div className="scope-group" role="group" aria-label={title}>
      <h4>{title}</h4>
      {dimensions.length === 0 ? (
        <p className="empty-copy">{empty}</p>
      ) : (
        <ul>
          {dimensions.map((dimension) => {
            const item = coverageDimensionPresentation(dimension);
            return (
              <li key={`${dimension.kind}:${dimension.value}`}>
                <span>{item.label}</span>
                <strong>{item.value}</strong>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

function NextStep({ text }: { text: string }) {
  return (
    <aside className="next-step" aria-label="Recommended next step">
      <span>Next step</span>
      <p>{text}</p>
    </aside>
  );
}

function sourceStateLabel(state: CoverageSourceState): string {
  switch (state) {
    case "expected-unverified":
      return "Expected, not verified";
    case "current":
      return "Current";
    case "permission-required":
      return "Permission needed";
    case "unavailable":
      return "Unavailable";
    case "degraded":
      return "Degraded";
    case "stale":
      return "Stale";
    case "disconnected":
      return "Disconnected";
    case "unknown":
      return "Unknown";
  }
}

function humanize(value: string): string {
  return value.replace(/[._-]+/g, " ");
}
