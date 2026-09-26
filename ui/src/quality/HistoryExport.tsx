import { useState } from "react";

interface Props<T> {
  data: T;
  label: string;
  filename: string;
  privacy: string;
  serialize: (data: T) => string;
}

export function HistoryExport<T>({ data, label, filename, privacy, serialize }: Props<T>) {
  const [json, setJSON] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);

  function review() {
    try {
      setJSON(serialize(data));
      setFailed(false);
    } catch {
      setJSON(null);
      setFailed(true);
    }
  }

  function save() {
    if (json === null) return;
    const url = URL.createObjectURL(new Blob([json], { type: "application/json" }));
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = filename;
    anchor.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 0);
  }

  return <div className="quality-history-export">
    <h3>Save this {label} history snapshot</h3>
    <p>{privacy} This file contains only the records in this bounded read, with its original window and truncation flags. It is not a complete history or backup. Review the exact JSON before saving it locally; Cozy SOC neither rereads the controller nor uploads it.</p>
    <button type="button" onClick={review}>Review {label} history JSON</button>
    {failed ? <p role="alert">This history snapshot cannot be exported. Refresh the history list and try again.</p> : null}
    {json !== null ? <>
      <pre tabIndex={0} aria-label={`${label} history JSON preview`}>{json}</pre>
      <button type="button" onClick={save}>Save {label} history JSON</button>
    </> : null}
  </div>;
}
