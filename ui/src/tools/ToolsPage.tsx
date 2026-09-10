import type { ToolCapability, ToolsSnapshot } from "./tools";
import "./tools.css";

export interface ToolsPageProps {
  tools: ToolsSnapshot;
  onNavigate: (page: "devices" | "activity" | "coverage") => void;
}

export function ToolsPage({ tools, onNavigate }: ToolsPageProps) {
  return (
    <div className="tools-stack">
      <section className="product-card tools-controller" aria-labelledby="controller-title">
        <div>
          <p className="eyebrow">Controller</p>
          <h2 id="controller-title">Local Cozy SOC controller</h2>
          <p>Runtime identity is intentionally minimized here. Process IDs and controller credentials never enter the browser.</p>
        </div>
        <dl className="tools-facts">
          <Fact label="Build" value={tools.status.controller_version} />
          <Fact label="Started" value={formatTimestamp(tools.status.started_at)} />
          <Fact label="Config schema" value={`v${tools.status.config_schema_version}`} />
          <Fact label="Management transport" value="Protected local Unix socket" />
        </dl>
      </section>

      {tools.capabilities.length === 0 ? (
        <section className="product-card empty-product-state">
          <h2>No capabilities are registered</h2>
          <p>The controller is reachable, but its validated capability catalog is empty.</p>
        </section>
      ) : tools.capabilities.map((capability) => (
        <CapabilityCard key={capability.id} capability={capability} onNavigate={onNavigate} />
      ))}
    </div>
  );
}

function CapabilityCard({ capability, onNavigate }: { capability: ToolCapability; onNavigate: ToolsPageProps["onNavigate"] }) {
  return (
    <section className="product-card capability-card" aria-labelledby={`capability-${capability.id}`}>
      <header className="capability-card__header">
        <div>
          <p className="eyebrow">{ownershipLabel(capability.ownership)} · {capability.release}</p>
          <h2 id={`capability-${capability.id}`}>{capability.display_name}</h2>
          <p>{capability.summary}</p>
        </div>
        <span className={`status-pill status-pill--${capability.state.verification}`}>{verificationLabel(capability.state.verification)}</span>
      </header>

      <div className="capability-state-grid" aria-label={`${capability.display_name} operating state`}>
        <StateFact label="Monitoring intent" value={capability.state.desired === "enabled" ? "Enabled" : "Disabled"} detail={capability.configured ? "Saved configuration exists." : "Using the capability's disabled default."} />
        <StateFact label="Runtime" value={processLabel(capability)} detail={capability.health.process_required ? "Process state is separate from evidence verification." : "This capability has no independently managed engine process."} />
        <StateFact label="Verification" value={verificationLabel(capability.state.verification)} detail={capability.health.coverage_requires_verification ? "Coverage is still evaluated separately from this lifecycle state." : "This capability does not require coverage verification."} />
      </div>

      <div className="capability-sections">
        <section aria-labelledby={`${capability.id}-support`}>
          <h3 id={`${capability.id}-support`}>Platform support</h3>
          {capability.targets.length === 0 ? <p>No target platforms are declared.</p> : (
            <ul className="tool-list">
              {capability.targets.map((target, index) => (
                <li key={`${target.os}-${target.arch}-${index}`}>
                  <strong>{targetLabel(target.os, target.arch, target.min_version)}</strong>
                  <span>{supportLabel(target.support)}</span>
                </li>
              ))}
            </ul>
          )}
        </section>

        <section aria-labelledby={`${capability.id}-resources`}>
          <h3 id={`${capability.id}-resources`}>Resource evidence</h3>
          <p className="resource-status"><strong>{capability.resources.measurement === "measured" ? "Measured" : "Not measured yet"}</strong> · {capability.resources.profile}</p>
          {capability.resources.measurement === "measured" ? <MeasuredResources capability={capability} /> : null}
          {capability.resources.evidence ? <p>{capability.resources.evidence}</p> : null}
        </section>

        <section aria-labelledby={`${capability.id}-permissions`}>
          <h3 id={`${capability.id}-permissions`}>Permissions</h3>
          {capability.privileges.length === 0 ? <p>No additional privilege requirements are declared.</p> : (
            <ul className="tool-list">
              {capability.privileges.map((privilege) => (
                <li key={privilege.id}><strong>{requirementLabel(privilege.requirement)}</strong><span>{privilege.description}</span></li>
              ))}
            </ul>
          )}
        </section>
      </div>

      {capability.id === "device-watch" ? (
        <section className="capability-evidence-links" aria-labelledby={`${capability.id}-evidence`}>
          <div>
            <h3 id={`${capability.id}-evidence`}>Evidence lives in Cozy SOC</h3>
            <p>{capability.deep_link_count === 0 ? "Device Watch has no separate engine UI, so there is no broken external link to open." : "Declared external deep links are not exposed until their destinations and context rules are validated."}</p>
          </div>
          <div className="capability-actions">
            <button type="button" onClick={() => onNavigate("devices")}>View devices</button>
            <button type="button" onClick={() => onNavigate("activity")}>View activity</button>
            <button type="button" onClick={() => onNavigate("coverage")}>View coverage</button>
          </div>
        </section>
      ) : null}

      <details className="technical-details">
        <summary>Technical details</summary>
        <dl className="technical-grid">
          <Fact label="Capability ID" value={capability.id} />
          <Fact label="Ownership" value={capability.ownership} />
          <Fact label="Provenance" value={`${capability.provenance.kind} · ${capability.provenance.license}`} />
          <Fact label="Version policy" value={capability.provenance.version_policy} />
          <Fact label="Lifecycle actions" value={capability.lifecycle.length ? capability.lifecycle.join(", ") : "None declared"} />
          <Fact label="Verification signals" value={capability.health.verification_signals.length ? capability.health.verification_signals.join(", ") : "None declared"} />
        </dl>
      </details>
    </section>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return <div><dt>{label}</dt><dd>{value}</dd></div>;
}

function StateFact({ label, value, detail }: { label: string; value: string; detail: string }) {
  return <div className="capability-state"><span>{label}</span><strong>{value}</strong><p>{detail}</p></div>;
}

function MeasuredResources({ capability }: { capability: ToolCapability }) {
  const values: string[] = [];
  if (capability.resources.max_ram_mib !== undefined) values.push(`${capability.resources.max_ram_mib} MiB RAM budget`);
  if (capability.resources.max_disk_mib !== undefined) values.push(`${capability.resources.max_disk_mib} MiB disk budget`);
  if (capability.resources.max_cpu_percent !== undefined) values.push(`${capability.resources.max_cpu_percent}% CPU budget`);
  return <p>{values.length ? values.join(" · ") : "Measured status is declared, but no numeric presentation budgets are available."}</p>;
}

function processLabel(capability: ToolCapability): string {
  if (!capability.health.process_required && capability.state.process === "not-applicable") return "Built into Cozy SOC";
  switch (capability.state.process) {
    case "stopped": return "Stopped";
    case "starting": return "Starting";
    case "running": return "Running";
    case "failed": return "Failed";
    default: return "Not applicable";
  }
}

function verificationLabel(value: ToolCapability["state"]["verification"]): string {
  switch (value) {
    case "verified": return "Verification passed";
    case "verifying": return "Checking";
    case "degraded": return "Needs attention";
    case "stale": return "Stale";
    default: return "Not yet verified";
  }
}

function ownershipLabel(value: ToolCapability["ownership"]): string {
  switch (value) {
    case "builtin": return "Built in";
    case "external": return "Externally owned";
    case "managed-local": return "Managed locally";
    case "managed-remote": return "Managed remotely";
  }
}

function supportLabel(value: ToolCapability["targets"][number]["support"]): string {
  switch (value) {
    case "tested": return "Tested on this declared target.";
    case "limited": return "Limited support; known constraints remain.";
    case "planned": return "Planned; not currently supported.";
    case "candidate": return "Candidate; release validation is still pending.";
  }
}

function requirementLabel(value: ToolCapability["privileges"][number]["requirement"]): string {
  switch (value) {
    case "required": return "Required";
    case "conditional": return "May be required";
    case "none": return "Not required";
  }
}

function targetLabel(os: string, arch: string, minVersion?: string): string {
  const osLabel = os === "darwin" ? "macOS" : os === "linux" ? "Linux" : os === "windows" ? "Windows" : os;
  const archLabel = arch === "arm64" && os === "darwin" ? "Apple silicon" : arch;
  return `${osLabel}${minVersion ? ` ${minVersion}+` : ""} · ${archLabel}`;
}

function formatTimestamp(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "medium" }).format(new Date(value));
}
