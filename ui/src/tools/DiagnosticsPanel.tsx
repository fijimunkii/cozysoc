import { useEffect, useRef, useState } from "react";
import { loadDiagnosticPreview, type DiagnosticPreview } from "./diagnostics";

export interface DiagnosticsPanelProps {
  mode: "live" | "demo";
  load?: (signal: AbortSignal) => Promise<DiagnosticPreview>;
}

export function DiagnosticsPanel({ mode, load = loadDiagnosticPreview }: DiagnosticsPanelProps) {
  const [preview, setPreview] = useState<DiagnosticPreview | null>(null);
  const [state, setState] = useState<"idle" | "loading" | "error">("idle");
  const request = useRef<AbortController | null>(null);
  useEffect(() => () => request.current?.abort(), []);
  useEffect(() => {
    request.current?.abort();
    setPreview(null);
    setState("idle");
  }, [mode]);

  const read = async () => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setPreview(null);
    setState("loading");
    try {
      const result = await load(controller.signal);
      if (!controller.signal.aborted) {
        setPreview(result);
        setState("idle");
      }
    } catch {
      if (!controller.signal.aborted) setState("error");
    }
  };

  const download = () => {
    if (preview === null) return;
    const url = URL.createObjectURL(new Blob([JSON.stringify(preview, null, 2) + "\n"], { type: "application/json" }));
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = "cozysoc-diagnostic-preview.json";
    anchor.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 0);
  };

  return <section className="product-card" aria-labelledby="diagnostics-title">
    <p className="eyebrow">Local support</p>
    <h2 id="diagnostics-title">Diagnostic preview</h2>
    <p>Review a small versioned support snapshot before saving it. It contains build and module state, controller gaps, a category for Device Watch coverage failures, and separate database-quota and host-volume health states. It excludes storage sizes, network names, addresses, URLs, paths, credentials, and raw errors. Nothing is uploaded.</p>
    {mode === "demo" ? <p>Connect to the local controller to preview real diagnostics.</p> : <>
      <button className="diagnostics-action" type="button" onClick={() => void read()} disabled={state === "loading"}>{state === "loading" ? "Preparing preview…" : preview ? "Refresh preview" : "Preview diagnostics"}</button>
      {state === "error" ? <p role="alert">Diagnostic preview is unavailable. Try again while the local controller is running.</p> : null}
      {preview ? <>
        <p>Review these exact fields before saving a local copy:</p>
        <pre className="diagnostic-preview" aria-label="Diagnostic bundle preview" tabIndex={0}>{JSON.stringify(preview, null, 2)}</pre>
        <button className="diagnostics-action" type="button" onClick={download}>Save preview as JSON</button>
      </> : null}
    </>}
  </section>;
}
