import { useEffect, useRef, useState } from "react";

import type { AppData } from "../app-data";
import { coverageStatePresentation } from "../coverage/presentation";
import type { NetworkInterface, SetupClient } from "./setup";
import { SetupRequestError } from "./setup";
import "./setup.css";

type SetupAction = "enroll" | "enable" | "disable";

export function SetupPanel({ data, client, onChanged, onReviewCoverage }: { data: AppData; client: SetupClient; onChanged: () => void; onReviewCoverage: () => void }) {
  const [dismissed, setDismissed] = useState(false);
  const [selected, setSelected] = useState("");
  const [reviewedNetwork, setReviewedNetwork] = useState<NetworkInterface | null>(null);
  const [reviewInvalidated, setReviewInvalidated] = useState(false);
  const [confirmingDisable, setConfirmingDisable] = useState(false);
  const [pending, setPending] = useState<SetupAction | null>(null);
  const [error, setError] = useState<{ action: SetupAction; message: string } | null>(null);

  const enrolled = data.networks.enrolled;
  const enabled = enrolled !== undefined && data.devices?.configured === true;
  const selectedNetwork = data.networks.candidates.find((candidate) => candidate.interface_name === selected);
  const reviewMatches = reviewedNetwork !== null && selectedNetwork !== undefined && sameNetwork(reviewedNetwork, selectedNetwork);
  const canAuthorize = reviewMatches && !reviewInvalidated && data.network_error === undefined && data.devices !== null;
  const stage = data.network_error !== undefined || data.devices === null ? "unavailable" : enrolled === undefined ? "choose" : enabled ? "verify" : "enable";
  const heading = useRef<HTMLHeadingElement>(null);
  const pauseHeading = useRef<HTMLHeadingElement>(null);
  const enrollmentReviewHeading = useRef<HTMLHeadingElement>(null);
  const disableReviewHeading = useRef<HTMLHeadingElement>(null);
  const reviewSelectionButton = useRef<HTMLButtonElement>(null);
  const pauseDeviceWatchButton = useRef<HTMLButtonElement>(null);
  const panel = dismissed ? "paused" : data.network_error !== undefined || data.devices === null ? "stage" : reviewedNetwork !== null && enrolled === undefined ? "enrollment-review" : confirmingDisable && enabled ? "disable-review" : "stage";
  const previousPanel = useRef(panel);
  const previousRead = useRef(data);
  const previousStage = useRef(stage);
  const focusAfterMutation = useRef(false);

  useEffect(() => {
    if (previousRead.current === data) return;
    if (focusAfterMutation.current && previousStage.current !== stage) heading.current?.focus();
    focusAfterMutation.current = false;
    previousRead.current = data;
    previousStage.current = stage;
  }, [data, stage]);

  useEffect(() => {
    if (reviewedNetwork !== null && (data.network_error !== undefined || data.devices === null || !reviewMatches)) setReviewInvalidated(true);
  }, [data.network_error, data.devices, reviewedNetwork, reviewMatches]);

  useEffect(() => {
    if (enrolled !== undefined && reviewedNetwork !== null) setReviewedNetwork(null);
  }, [enrolled, reviewedNetwork]);

  useEffect(() => {
    if (previousPanel.current === panel) return;
    if (panel === "paused") pauseHeading.current?.focus();
    else if (panel === "enrollment-review") enrollmentReviewHeading.current?.focus();
    else if (panel === "disable-review") disableReviewHeading.current?.focus();
    else if (previousPanel.current === "paused") heading.current?.focus();
    else if (previousPanel.current === "enrollment-review") reviewSelectionButton.current?.focus();
    else if (previousPanel.current === "disable-review") pauseDeviceWatchButton.current?.focus();
    previousPanel.current = panel;
  }, [panel]);

  const onMutationChanged = () => {
    focusAfterMutation.current = true;
    onChanged();
  };

  if (dismissed) {
    return (
      <section className="product-card setup-paused" aria-labelledby="setup-paused-title">
        <div>
          <p className="eyebrow">Setup paused</p>
          <h2 ref={pauseHeading} id="setup-paused-title" tabIndex={-1}>Continue when you are ready</h2>
          <p>No network or monitoring setting changes while setup is paused.</p>
        </div>
        <button type="button" className="primary-action" onClick={() => setDismissed(false)}>Resume setup</button>
      </section>
    );
  }

  if (data.network_error !== undefined || data.devices === null) {
    return (
      <section className="product-card setup-panel" aria-labelledby="setup-title">
        <header className="setup-header">
          <div>
            <p className="eyebrow">Setup</p>
            <h2 ref={heading} id="setup-title" tabIndex={-1}>{data.network_error !== undefined ? "Network setup information is unavailable" : "Device status is unavailable"}</h2>
            <p>{data.network_error ?? "Current Device Watch state could not be read."} {data.devices === null ? "Coverage remains available; setup actions are paused so monitoring intent is not guessed." : "Existing device and coverage evidence remains available; no network authorization is changed."}</p>
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
            <h2 ref={heading} id="setup-title" tabIndex={-1}>Choose the home network to authorize</h2>
            <p>Cozy SOC will not select a network automatically. Authorization only records the scope you choose; Device Watch stays off until you enable it separately. This choice and resulting device evidence stay on this machine by default; no account or router change is required.</p>
          </div>
          <span className="setup-step">Step 1 of 3</span>
        </header>

        {error ? <SetupErrorView error={error} onDismiss={() => setError(null)} /> : null}

        {reviewedNetwork ? (
          <div className="setup-confirmation">
            <p className="setup-kicker">Review authorization</p>
            <h3 ref={enrollmentReviewHeading} tabIndex={-1}>Authorize {reviewedNetwork.interface_name}?</h3>
            <p>This authorizes Device Watch to use this interface and its currently observed local prefixes as the home-network scope. It does not start monitoring yet.</p>
            <NetworkDetail network={reviewedNetwork} />
            {!canAuthorize ? <p role="alert">The interface or local prefixes changed since this review. Go Back and review the current network before authorizing.</p> : null}
            <div className="setup-actions">
              <button type="button" className="secondary-action" disabled={pending !== null} onClick={() => setReviewedNetwork(null)}>Back</button>
              <button type="button" className="primary-action" disabled={pending !== null || !canAuthorize} onClick={() => {
                if (!canAuthorize) return;
                void run("enroll", () => client.enrollNetwork(reviewedNetwork), setPending, setError, onMutationChanged, (error) => {
                  if (error instanceof SetupRequestError && error.code === "precondition_failed") {
                    setReviewInvalidated(true);
                    onChanged();
                  }
                });
              }}>
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
              <button ref={reviewSelectionButton} type="button" className="primary-action" disabled={selectedNetwork === undefined || pending !== null} onClick={() => {
                if (selectedNetwork === undefined) return;
                setReviewedNetwork({ ...selectedNetwork, prefixes: [...selectedNetwork.prefixes] });
                setReviewInvalidated(false);
              }}>Review selection</button>
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
            <h2 ref={heading} id="setup-title" tabIndex={-1}>Enable Device Watch when you are ready</h2>
            <p>{enrolled.interface.interface_name} is authorized, but monitoring is still off. Enabling starts passive Device Watch only after controller preflight succeeds.</p>
          </div>
          <span className="setup-step">Step 2 of 3</span>
        </header>
        <div className="setup-body">
          <NetworkDetail network={enrolled.interface} />
          {error ? <SetupErrorView error={error} onDismiss={() => setError(null)} /> : null}
          <div className="setup-actions setup-actions--split">
            <button type="button" className="quiet-button" onClick={() => setDismissed(true)}>Not now</button>
            <button type="button" className="primary-action" disabled={pending !== null} onClick={() => void run("enable", () => client.enableDeviceWatch(), setPending, setError, onMutationChanged)}>
              {pending === "enable" ? "Enabling…" : "Enable Device Watch"}
            </button>
          </div>
        </div>
      </section>
    );
  }

  const report = data.coverage.reports.find((item) => item.capability_id === "device-watch");
  const coverage = report === undefined ? undefined : coverageStatePresentation(report.state);
  const verified = report?.state === "active-limited";
  const awaitingEvidence = report === undefined || report.state === "unverified";
  return (
    <section className={`product-card setup-panel${verified ? " setup-panel--complete" : ""}`} aria-labelledby="setup-title">
      <header className="setup-header">
        <div>
          <p className="eyebrow">Device Watch</p>
          <h2 ref={heading} id="setup-title" tabIndex={-1}>{verified ? "Device Watch is reporting current evidence" : awaitingEvidence ? "Verify Device Watch coverage" : "Device Watch coverage needs review"}</h2>
          <p>{enrolled.interface.interface_name} is authorized and Device Watch is enabled. {verified
            ? "Current passive neighbor evidence supports limited local visibility, not complete network or traffic monitoring."
            : "Enabled intent does not establish current visibility; check the reported evidence and gaps before relying on it."}</p>
        </div>
        <span className="setup-step">{verified ? "Setup complete" : awaitingEvidence ? "Step 3 of 3" : "Coverage needs review"}</span>
      </header>
      <div className="setup-body">
        <NetworkDetail network={enrolled.interface} />
        <div className="setup-verification" aria-live="polite">
          <strong>{verified ? "Current limited coverage verified" : awaitingEvidence ? "Waiting for current coverage evidence" : `Current coverage: ${coverage?.label ?? "No report yet"}`}</strong>
          <p>{verified
            ? "Review the Coverage view for the observation point, evidence time, and known gaps."
            : report?.next_step ?? "Refresh local evidence after the first passive collection, then review Coverage for scope and gaps."}</p>
          <div className="setup-actions">
            {!verified ? <button type="button" className="secondary-action" onClick={onChanged}>Refresh evidence</button> : null}
            <button type="button" className="secondary-action" onClick={onReviewCoverage}>Review coverage evidence</button>
          </div>
        </div>
        {error ? <SetupErrorView error={error} onDismiss={() => setError(null)} /> : null}
        {confirmingDisable ? (
          <div className="setup-confirmation setup-confirmation--inline">
            <h3 ref={disableReviewHeading} tabIndex={-1}>Disable Device Watch?</h3>
            <p>This stops Device Watch monitoring intent but keeps the network authorization so you can enable it again later.</p>
            <div className="setup-actions">
              <button type="button" className="secondary-action" disabled={pending !== null} onClick={() => setConfirmingDisable(false)}>Back</button>
              <button type="button" className="danger-action" disabled={pending !== null} onClick={() => void run("disable", () => client.disableDeviceWatch(), setPending, setError, onMutationChanged)}>
                {pending === "disable" ? "Disabling…" : "Disable Device Watch"}
              </button>
            </div>
          </div>
        ) : (
          <div className="setup-actions setup-actions--end">
            <button ref={pauseDeviceWatchButton} type="button" className="secondary-action" onClick={() => setConfirmingDisable(true)}>Pause Device Watch</button>
          </div>
        )}
      </div>
    </section>
  );
}

function sameNetwork(left: NetworkInterface, right: NetworkInterface): boolean {
  return left.interface_name === right.interface_name
    && left.interface_index === right.interface_index
    && left.prefixes.length === right.prefixes.length
    && left.prefixes.every((prefix) => right.prefixes.includes(prefix));
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
  onFailure?: (error: unknown) => void,
): Promise<void> {
  setPending(action);
  setError(null);
  try {
    await operation();
    onChanged();
  } catch (error: unknown) {
    setError({ action, message: setupErrorMessage(action, error) });
    onFailure?.(error);
  } finally {
    setPending(null);
  }
}

function setupErrorMessage(action: SetupAction, error: unknown): string {
  if (!(error instanceof SetupRequestError)) return "The local setup operation failed. No additional change is assumed.";
  if (error.code === "precondition_failed") {
    if (action === "enroll") return "That interface or its local scope changed before enrollment. Refresh evidence, then review the current network again. No authorization was recorded.";
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
