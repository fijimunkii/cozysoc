import { useId, useRef, useState } from "react";
import { SetupRequestError, loadHTTPSSelectionsFromWeb, type HTTPSSettingsClient } from "../setup/setup";
import type { HTTPSSelection, HTTPSSettings } from "./https-check";
import "./local-quality.css";

type Draft = { endpoint: string; server_name: string; request_target: string; family: string; method: string; expected_status: string; destination_policy: string };
const empty: Draft = { endpoint: "", server_name: "", request_target: "", family: "", method: "", expected_status: "", destination_policy: "" };

export function HTTPSSettingsPanel({ client, loadSelections = loadHTTPSSelectionsFromWeb }: {
  client: HTTPSSettingsClient; loadSelections?: () => Promise<HTTPSSelection[]>;
}) {
  const titleID = useId();
  const [draft, setDraft] = useState<Draft>(empty);
  const [items, setItems] = useState<HTTPSSelection[] | null>(null);
  const [selected, setSelected] = useState("");
  const [retireCandidate, setRetireCandidate] = useState<HTTPSSelection | null>(null);
  const [message, setMessage] = useState("");
  const [uncertain, setUncertain] = useState(false);
  const busy = useRef(false);

  async function load() {
    if (busy.current) return;
    busy.current = true; setMessage("Reading saved HTTPS targets…"); setRetireCandidate(null);
    try {
      const next = await loadSelections();
      setItems(next); setSelected(next.some((item) => item.selection_id === selected) ? selected : next[0]?.selection_id ?? "");
      setUncertain(false); setMessage(next.length ? "Saved HTTPS targets are current." : "No HTTPS target is saved for the enrolled network.");
    } catch { setMessage("Saved HTTPS targets could not be read. Check the controller connection and enrollment."); }
    finally { busy.current = false; }
  }

  async function save() {
    if (busy.current || uncertain) return;
    if (Object.values(draft).some((value) => !value.trim())) { setMessage("Complete every HTTPS setting before saving."); return; }
    const status = Number(draft.expected_status);
    if (!Number.isInteger(status) || status < 100 || status > 599) { setMessage("Choose an HTTP status from 100 to 599."); return; }
    const settings: HTTPSSettings = { endpoint: draft.endpoint, server_name: draft.server_name, request_target: draft.request_target,
      family: draft.family as HTTPSSettings["family"], method: draft.method as HTTPSSettings["method"], expected_status: status,
      destination_policy: draft.destination_policy as HTTPSSettings["destination_policy"] };
    busy.current = true; setMessage("Saving this HTTPS target without sending a request…");
    try {
      const saved = await client.saveHTTPSSettings(settings);
      setItems((current) => current ? [...current, saved] : [saved]);
      setSelected(saved.selection_id); setDraft(empty);
      setMessage("HTTPS target saved. Saving did not send a request or approve a check. Load selections in the check panel when ready to review one.");
    } catch (error) {
      const invalid = error instanceof SetupRequestError && ["invalid_request", "invalid_settings"].includes(error.code);
      setUncertain(!invalid);
      setMessage(invalid ? "These HTTPS settings are outside the supported profile. Correct them before saving." :
        "The save outcome could not be confirmed. Load saved HTTPS targets before deciding whether to try again; do not automatically retry.");
    } finally { busy.current = false; }
  }

  async function retire() {
    if (busy.current || uncertain || !retireCandidate || !items?.some((item) => item.selection_id === retireCandidate.selection_id)) return;
    busy.current = true; setMessage("Retiring the selected HTTPS target…");
    try {
      await client.retireHTTPSSettings(retireCandidate.selection_id);
      const remaining = items.filter((item) => item.selection_id !== retireCandidate.selection_id);
      setItems(remaining); setSelected(remaining[0]?.selection_id ?? ""); setRetireCandidate(null);
      setMessage("HTTPS target retired. Existing audit references remain in local storage.");
    } catch {
      setUncertain(true); setRetireCandidate(null);
      setMessage("The retirement outcome could not be confirmed. Reload saved HTTPS targets before deciding what to do next; do not automatically retry.");
    } finally { busy.current = false; }
  }

  return <section className="product-card local-quality gateway-check" aria-labelledby={titleID}>
    <p className="eyebrow">Network quality · saved targets</p><h2 id={titleID}>Set up an HTTPS check target</h2>
    <p>Save an exact numeric endpoint, TLS identity and HTTP request for the enrolled network. Saving only stores local settings; it sends no HTTPS traffic and grants no check approval. A later one-shot check needs its own review and consent.</p>
    <form onSubmit={(event) => { event.preventDefault(); void save(); }}>
      <div className="check-settings-fields">
        <label>Numeric server IP and port 443<input required maxLength={64} placeholder="192.168.1.10:443" value={draft.endpoint} onChange={(event) => setDraft({ ...draft, endpoint: event.target.value })} /></label>
        <label>TLS server name and HTTP Host<input required maxLength={253} placeholder="private.example" value={draft.server_name} onChange={(event) => setDraft({ ...draft, server_name: event.target.value })} /></label>
        <label>Exact path and optional query<input required maxLength={1024} placeholder="/health" value={draft.request_target} onChange={(event) => setDraft({ ...draft, request_target: event.target.value })} /></label>
        <label>Address family<select required value={draft.family} onChange={(event) => setDraft({ ...draft, family: event.target.value })}><option value="">Choose a family</option><option value="ipv4">IPv4</option><option value="ipv6">IPv6</option></select></label>
        <label>HTTP method<select required value={draft.method} onChange={(event) => setDraft({ ...draft, method: event.target.value })}><option value="">Choose a method</option><option value="HEAD">HEAD</option><option value="GET">GET</option></select></label>
        <label>Expected final HTTP status<input required type="number" min="100" max="599" step="1" value={draft.expected_status} onChange={(event) => setDraft({ ...draft, expected_status: event.target.value })} /></label>
        <label>Destination policy<select required value={draft.destination_policy} onChange={(event) => setDraft({ ...draft, destination_policy: event.target.value })}><option value="">Choose a policy</option><option value="exact-endpoint">Exact endpoint</option></select></label>
      </div>
      <button type="submit" className="primary-action" disabled={busy.current || uncertain}>Save HTTPS target</button>
    </form>
    <p>The destination can observe a later check. The server name, path and query are stored in private local controller state. Use a path without credentials or household secrets.</p>
    <button type="button" onClick={() => { void load(); }} disabled={busy.current}>Load saved HTTPS targets</button>
    {items?.length ? <div>
      <label>Saved HTTPS target{" "}<select value={selected} disabled={busy.current} onChange={(event) => { setSelected(event.target.value); setRetireCandidate(null); }}>
        {items.map((item) => <option key={item.selection_id} value={item.selection_id}>{item.settings.method} {item.settings.server_name}{item.settings.request_target} via {item.settings.endpoint}</option>)}
      </select></label>{" "}
      <button type="button" disabled={busy.current || uncertain} onClick={() => setRetireCandidate(items.find((item) => item.selection_id === selected) ?? null)}>Review retirement</button>
    </div> : null}
    {retireCandidate ? <div aria-label="HTTPS target retirement review"><p>Retire {retireCandidate.settings.method} {retireCandidate.settings.server_name}{retireCandidate.settings.request_target} via {retireCandidate.settings.endpoint}? This saved target can no longer be selected for a new check. Existing local audits remain.</p>
      <button type="button" onClick={() => { void retire(); }} disabled={busy.current || uncertain}>Retire this HTTPS target</button>{" "}
      <button type="button" onClick={() => setRetireCandidate(null)}>Cancel</button>
    </div> : null}
    {message ? <p role={uncertain ? "alert" : "status"}>{message}</p> : null}
  </section>;
}
