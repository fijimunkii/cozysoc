import { useState } from "react";

import type { AppData } from "../app-data";
import { coverageStatePresentation } from "../coverage/presentation";
import type { SetupClient } from "./setup";
import { SetupRequestError } from "./setup";
import "./setup.css";

type SetupAction = "enroll" | "enable" | "disable";

export function SetupPanel({ data, client, onChanged }: { data: AppData; client: SetupClient; onChanged: () => void }) {
  const [dismissed, setDismissed] = useState(false);
  const [selected, setSelected] = useState("");
  const [confirmingEnrollment, setConfirmingEnrollment] = useState(false);
  const [confirmingDisable, setConfirmingDisable] = useState(false);
  const [pending, setPending] = useState<SetupAction | null>(null);
  const [error, setError] = useState<{ action: SetupAction; message: string } | null>(null);

  const enrolled = data.networks.enrolled;
  const enabled = enrolled !== undefined && data.devices.configured;
  const selectedNetwork = data.networks.candidates.find((candidate) => candidate.interface_name === selected);

  if (dismissed) {
    return (
      <section className="product-card setup-paused" aria-labelledby="setup-paused-title">
        <div>
          <p className="eyebrow">Setup paused</p>
          <h2 id="setup-paused-title">Continue when you are ready</h2>
          <p>No network or monitoring setting changes while setup is paused.</p>
        </div>
        <button type="button" className="primary-action" onClick={() => setDismissed(false)}>Resume setup</button>
      </section>
    );
  }

  if (data.network_error !== undefined) {
    return (
      <section className="product-card setup-panel" aria-labelledby="setup-title">
        <header className="setup-header">
          <div>
            <p className="eyebrow">Setup</p>
            <h2 id="setup-title">Network setup information is unavailable</h2>
            <p>{data.network_error} Existing device and coverage evidence remains available; no network authorization is changed.</p>
          </div>
        </header>
        <div className="setup-actions setup-actions--end setup-body">
          <button type="button" className="secondary-action" onClick={onChanged}>Retry setup information</button>
        </div>
      </section>
    );
  }

  if (enrolled === undefined) {
    return (
      <section className="product-card setup-panel" aria-labelledby="setup-title">
        <header className="setup-header">
          <div>
            <p className="eyebrow">First-time setup</p>
            <h2 id="setup-title">Choose the home network to authorize</h2>
            <p>Cozy SOC will not select a network automatically. Authorization only records the scope you choose; Device Watch stays off until you enable it separately.</p>
          </div>
          <span className="setup-step">Step 1 of 2</span>
        </header>

        {error ? <SetupErrorView error={error} onDismiss={() => setError(null)} /> : null}

        {confirmingEnrollment && selectedNetwork ? (
          <div className="setup-confirmation">
            <p className="setup-kicker">Review authorization</p>
            <h3>Authorize {selectedNetwork.interface_name}?</h3>
            <p>This authorizes Device Watch to use this interface and its currently observed local prefixes as the home-network scope. It does not start monitoring yet.</p>
            <NetworkDetail network={selectedNetwork} />
            <div className="setup-actions">
              <button type="button" className="secondary-action" disabled={pending !== null} onClick={() => setConfirmingEnrollment(false)}>Back</button>
              <button type="button" className="primary-action" disabled={pending !== null} onClick={() => void run("enroll", () => client.enrollNetwork(selectedNetwork.interface_name), setPending, setError, onChanged)}>
                {pending === "enroll" ? "Authorizing…" : "Authorize this network"}
              </button>
            </div>
          </div>
        ) : (
          <div className="setup-body">
            {data.networks.candidates.length === 0 ? (
              <div className="setup-empty">
                <strong>No eligible local network interfaces found</strong>
                <p>Connect this computer to the home network you want to observe, then refresh. Cozy SOC will not probe another network to guess.</p>
                <button type="button" className="secondary-action" onClick={onChanged}>Refresh network list</button>
              </div>
            ) : (
              <fieldset className="network-choices">
                <legend>Select one network interface</legend>
                {data.networks.candidates.map((candidate) => (
                  <label key={candidate.interface_name} className={`network-choice${selected === candidate.interface_name ? " network-choice--selected" : ""}`}>
                    <input
                      type="radio"
                      name="home-network"
                      value={candidate.interface_name}
                      checked={selected === candidate.interface_name}
                      onChange={() => setSelected(candidate.interface_name)}
                    />
                    <span className="network-choice-copy">
                      <strong>{candidate.interface_name}</strong>
                      <span>{candidate.prefixes.join(" · ")}</span>
                    </span>
                  </label>
                ))}
              </fieldset>
            )}
            {data.networks.candidates_truncated ? <p className="setup-note" role="status">Only the first bounded set of eligible interfaces is shown.</p> : null}
            <div className="setup-actions setup-actions--split">
              <button type="button" className="quiet-button" onClick={() => setDismissed(true)}>Not now</button>
              <button type="button" className="primary-action" disabled={selectedNetwork === undefined || pending !== null} onClick={() => setConfirmingEnrollment(true)}>Review selection</button>
            </div>
          </div>
        )}
      </section>
    );
  }

  if (!enabled) {
    return (
      <section className="product-card setup-panel" aria-labelledby="setup-title">
        <header className="setup-header">
          <div>
            <p className="eyebrow">Home network authorized</p>
            <h2 id="setup-title">Enable Device Watch when you are ready</h2>
            <p>{enrolled.interface.interface_name} is authorized, but monitoring is still off. Enabling starts passive Device Watch only after controller preflight succeeds.</p>
          </div>
          <span className="setup-step">Step 2 of 2</span>
        </header>
        <div className="setup-body">
          <NetworkDetail network={enrolled.interface} />
          {error ? <SetupErrorView error={error} onDismiss={() => setError(null)} /> : null}
          <div className="setup-actions setup-actions--split">
            <button type="button" className="quiet-button" onClick={() => setDismissed(true)}>Not now</button>
            <button type="button" className="primary-action" disabled={pending !== null} onClick={() => void run("enable", () => client.enableDeviceWatch(), setPending, setError, onChanged)}>
              {pending === "enable" ? "Enabling…" : "Enable Device Watch"}
            </button>
          </div>
        </div>
      </section>
    );
  }

  const report = data.coverage.reports.find((item) => item.capability_id === "device-watch");
  const coverage = report === undefined ? undefined : coverageStatePresentation(report.state);
  return (
    <section className="product-card setup-panel setup-panel--complete" aria-labelledby="setup-title">
      <header className="setup-header">
        <div>
          <p className="eyebrow">Device Watch</p>
          <h2 id="setup-title">Monitoring is enabled for {enrolled.interface.interface_name}</h2>
          <p>Coverage still determines whether evidence is current and what remains out of view. Enabled intent is not presented as proof of complete monitoring.</p>
        </div>
        <span className="setup-step">Setup complete</span>
      </header>
      <div className="setup-body">
        <NetworkDetail network={enrolled.interface} />
        {coverage ? <p className="setup-note">Current coverage: <strong>{coverage.label}</strong>. Review Coverage for evidence freshness and known gaps.</p> : null}
        {error ? <SetupErrorView error={error} onDismiss={() => setError(null)} /> : null}
        {confirmingDisable ? (
          <div className="setup-confirmation setup-confirmation--inline">
            <h3>Disable Device Watch?</h3>
            <p>This stops Device Watch monitoring intent but keeps the network authorization so you can enable it again later.</p>
            <div className="setup-actions">
              <button type="button" className="secondary-action" disabled={pending !== null} onClick={() => setConfirmingDisable(false)}>Back</button>
              <button type="button" className="danger-action" disabled={pending !== null} onClick={() => void run("disable", () => client.disableDeviceWatch(), setPending, setError, onChanged)}>
                {pending === "disable" ? "Disabling…" : "Disable Device Watch"}
              </button>
            </div>
          </div>
        ) : (
          <div className="setup-actions setup-actions--end">
            <button type="button" className="secondary-action" onClick={() => setConfirmingDisable(true)}>Pause Device Watch</button>
          </div>
        )}
      </div>
    </section>
  );
}

function NetworkDetail({ network }: { network: AppData["networks"]["candidates"][number] }) {
  return (
    <dl className="network-detail">
      <div><dt>Interface</dt><dd>{network.interface_name}</dd></div>
      <div><dt>Local scope</dt><dd>{network.prefixes.join(", ")}</dd></div>
    </dl>
  );
}

function SetupErrorView({ error, onDismiss }: { error: { action: SetupAction; message: string }; onDismiss: () => void }) {
  return (
    <div className="setup-error" role="alert">
      <div><strong>Setup change was not applied</strong><p>{error.message}</p></div>
      <button type="button" className="quiet-button" onClick={onDismiss}>Dismiss</button>
    </div>
  );
}

async function run<T>(
  action: SetupAction,
  operation: () => Promise<T>,
  setPending: (value: SetupAction | null) => void,
  setError: (value: { action: SetupAction; message: string } | null) => void,
  onChanged: () => void,
): Promise<void> {
  setPending(action);
  setError(null);
  try {
    await operation();
    onChanged();
  } catch (error: unknown) {
    setError({ action, message: setupErrorMessage(action, error) });
  } finally {
    setPending(null);
  }
}

function setupErrorMessage(action: SetupAction, error: unknown): string {
  if (!(error instanceof SetupRequestError)) return "The local setup operation failed. No additional change is assumed.";
  if (error.code === "precondition_failed") {
    if (action === "enroll") return "That interface is no longer eligible for enrollment. Refresh the network list and choose again.";
    if (action === "enable") return "Device Watch could not be enabled because a prerequisite is not satisfied. The network remains authorized, but monitoring was not enabled.";
    return "Device Watch could not be disabled because a prerequisite is not satisfied. Its previous state remains in effect.";
  }
  if (error.code === "conflict") return "The requested change conflicts with current controller state. Refresh this page to review the current authorization and monitoring state.";
  if (error.code === "unsupported_operation") return "This controller does not support this setup operation. Existing authorization and monitoring state were not changed.";
  if (error.code === "web_session_required") return "The local web session expired. Reopen the authenticated URL printed by `cozysoc web`.";
  if (error.code === "csrf_required" || error.code === "origin_required") return "The local setup session could not authorize this change. Reload the authenticated Cozy SOC page and try again.";
  if (error.code === "controller_unavailable") return "The local controller could not complete this change. Retry when the controller is available.";
  return error.message;
}
