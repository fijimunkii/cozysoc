export const maxWebJSONBytes = 4 * 1024 * 1024;
export const maxWebErrorJSONBytes = 8 * 1024;

// Bound bytes before decoding or parsing any browser-facing JSON response.
export async function readBoundedWebJSON(response: Response, maxBytes = maxWebJSONBytes): Promise<unknown> {
  const body = response.body;
  if (response.headers.get("content-type")?.split(";")[0]?.trim().toLowerCase() !== "application/json" || !body) {
    await body?.cancel().catch(() => undefined);
    throw new Error("Local JSON response is invalid.");
  }
  const length = response.headers.get("content-length");
  if (length !== null && /^\d+$/.test(length) && Number(length) > maxBytes) {
    await body.cancel().catch(() => undefined);
    throw new Error("Local JSON response exceeds its size limit.");
  }
  const reader = body.getReader();
  const decoder = new TextDecoder("utf-8", { fatal: true });
  let bytes = 0;
  let value = "";
  try {
    for (;;) {
      const chunk = await reader.read();
      if (chunk.done) break;
      bytes += chunk.value.byteLength;
      if (bytes > maxBytes) throw new Error("Local JSON response exceeds its size limit.");
      value += decoder.decode(chunk.value, { stream: true });
    }
    value += decoder.decode();
    return JSON.parse(value) as unknown;
  } catch {
    throw new Error("Local JSON response is invalid or exceeds its size limit.");
  } finally {
    await reader.cancel().catch(() => undefined);
    reader.releaseLock();
  }
}
