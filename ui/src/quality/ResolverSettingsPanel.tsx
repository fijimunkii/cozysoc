import { useId, useRef, useState } from "react";
import { SetupRequestError, loadResolverSelectionsFromWeb, type ResolverSettingsClient } from "../setup/setup";
import type { ResolverSelection, ResolverSettings } from "./resolver-check";
import "./local-quality.css";

type Draft = { [K in keyof ResolverSettings]: string };
const empty: Draft = { endpoint: "", name: "", family: "", transport: "", query_type: "", expect: "", destination_scope: "" };

export function ResolverSettingsPanel({ client, loadSelections = loadResolverSelectionsFromWeb }: {
  client: ResolverSettingsClient; loadSelections?: () => Promise<ResolverSelection[]>;
}) {
  const titleID = useId();
  const [settings, setSettings] = useState<Draft>(empty);
  const [items, setItems] = useState<ResolverSelection[] | null>(null);
  const [selected, setSelected] = useState("");
  const [retireCandidate, setRetireCandidate] = useState<ResolverSelection | null>(null);
  const [message, setMessage] = useState("");
  const [uncertain, setUncertain] = useState(false);
  const busy = useRef(false);

  async function load() {
    if (busy.current) return;
    busy.current = true; setMessage("Reading saved DNS targets…"); setRetireCandidate(null);
    try {
      const next = await loadSelections();
      setItems(next); setSelected(next.some((item) => item.selection_id === selected) ? selected : next[0]?.selection_id ?? "");
      setUncertain(false); setMessage(next.length ? "Saved DNS targets are current." : "No DNS target is saved for the enrolled network.");
    } catch { setMessage("Saved DNS targets could not be read. Check the controller connection and enrollment."); }
    finally { busy.current = false; }
  }

  async function save() {
    if (busy.current || uncertain) return;
    if (Object.values(settings).some((value) => !value.trim())) { setMessage("Complete every DNS setting before saving."); return; }
    busy.current = true; setMessage("Saving this DNS target without sending a query…");
    try {
      const saved = await client.saveResolverSettings(settings as unknown as ResolverSettings);
      setItems((current) => current ? [...current, saved] : [saved]);
      setSelected(saved.selection_id);
      setSettings(empty);
      setMessage("DNS target saved. Saving did not send a query or approve a check. Load selections in the check panel when ready to review one.");
    } catch (error) {
      const invalid = error instanceof SetupRequestError && ["invalid_request", "invalid_settings"].includes(error.code);
      setUncertain(!invalid);
      setMessage(invalid ? "These DNS settings are outside the supported profile. Correct them before saving." :
        "The save outcome could not be confirmed. Load saved DNS targets before deciding whether to try again; do not automatically retry.");
    } finally { busy.current = false; }
  }

  async function retire() {
    if (busy.current || uncertain || !retireCandidate || !items?.some((item) => item.selection_id === retireCandidate.selection_id)) return;
    busy.current = true; setMessage("Retiring the selected DNS target…");
    try {
      await client.retireResolverSettings(retireCandidate.selection_id);
      const remaining = items.filter((item) => item.selection_id !== retireCandidate.selection_id);
      setItems(remaining); setSelected(remaining[0]?.selection_id ?? ""); setRetireCandidate(null);
      setMessage("DNS target retired. Existing audit references remain in local storage.");
    } catch {
      setUncertain(true); setRetireCandidate(null);
      setMessage("The retirement outcome could not be confirmed. Reload saved DNS targets before deciding what to do next; do not automatically retry.");
    } finally { busy.current = false; }
  }

  return <section className="product-card local-quality gateway-check" aria-labelledby={titleID}>
    <p className="eyebrow">Network quality · saved targets</p><h2 id={titleID}>Set up a DNS check target</h2>
    <p>Save an exact resolver endpoint and question for the enrolled network. Saving only stores local settings; it sends no DNS traffic and grants no check approval. A later one-shot check needs its own review and consent.</p>
    <form onSubmit={(event) => { event.preventDefault(); void save(); }}>
      <div className="check-settings-fields">
        <label>Numeric resolver IP and port 53<input required maxLength={64} placeholder="192.168.1.1:53" value={settings.endpoint} onChange={(event) => setSettings({ ...settings, endpoint: event.target.value })} /></label>
        <label>Fully qualified query name<input required maxLength={254} placeholder="example.org." value={settings.name} onChange={(event) => setSettings({ ...settings, name: event.target.value })} /></label>
        <label>Address family<select required value={settings.family} onChange={(event) => setSettings({ ...settings, family: event.target.value })}><option value="">Choose a family</option><option value="ipv4">IPv4</option><option value="ipv6">IPv6</option></select></label>
        <label>Transport<select required value={settings.transport} onChange={(event) => setSettings({ ...settings, transport: event.target.value })}><option value="">Choose a transport</option><option value="udp">UDP</option></select></label>
        <label>Query type<select required value={settings.query_type} onChange={(event) => setSettings({ ...settings, query_type: event.target.value })}><option value="">Choose a query type</option><option value="A">A</option><option value="AAAA">AAAA</option></select></label>
        <label>Expected answer<select required value={settings.expect} onChange={(event) => setSettings({ ...settings, expect: event.target.value })}><option value="">Choose an expectation</option><option value="answer">Answer</option><option value="nxdomain">NXDOMAIN</option><option value="no-data">No data</option></select></label>
        <label>Destination policy<select required value={settings.destination_scope} onChange={(event) => setSettings({ ...settings, destination_scope: event.target.value })}><option value="">Choose a policy</option><option value="exact-endpoint">Exact endpoint</option><option value="enrolled-prefix">Enrolled prefix</option></select></label>
      </div>
      <button type="submit" className="primary-action" disabled={busy.current || uncertain}>Save DNS target</button>
    </form>
    <p>A resolver may forward this question upstream. Use a name you are comfortable disclosing to that resolver. The endpoint and name are stored in private local controller state.</p>
    <button type="button" onClick={() => { void load(); }} disabled={busy.current}>Load saved DNS targets</button>
    {items?.length ? <div>
      <label>Saved DNS target{" "}<select value={selected} disabled={busy.current} onChange={(event) => { setSelected(event.target.value); setRetireCandidate(null); }}>
        {items.map((item) => <option key={item.selection_id} value={item.selection_id}>{item.settings.endpoint} · {item.settings.query_type} {item.settings.name}</option>)}
      </select></label>{" "}
      <button type="button" disabled={busy.current || uncertain} onClick={() => setRetireCandidate(items.find((item) => item.selection_id === selected) ?? null)}>Review retirement</button>
    </div> : null}
    {retireCandidate ? <div aria-label="DNS target retirement review"><p>Retire {retireCandidate.settings.endpoint} · {retireCandidate.settings.query_type} {retireCandidate.settings.name}? This saved target can no longer be selected for a new check. Existing local audits remain.</p>
      <button type="button" onClick={() => { void retire(); }} disabled={busy.current || uncertain}>Retire this DNS target</button>{" "}
      <button type="button" onClick={() => setRetireCandidate(null)}>Cancel</button>
    </div> : null}
    {message ? <p role={uncertain ? "alert" : "status"}>{message}</p> : null}
  </section>;
}
