import { describe, expect, it } from "vitest";
import { readBoundedWebJSON } from "./web-json";

describe("bounded browser JSON reads", () => {
  it("accepts a streamed UTF-8 JSON response exactly at the byte limit", async () => {
    const body = '{"value":"é"}';
    const bytes = new TextEncoder().encode(body);
    const response = new Response(new ReadableStream({
      start(controller) {
        controller.enqueue(bytes.slice(0, 11));
        controller.enqueue(bytes.slice(11));
        controller.close();
      },
    }), { headers: { "Content-Type": "application/json; charset=utf-8" } });
    await expect(readBoundedWebJSON(response, bytes.length)).resolves.toEqual({ value: "é" });
  });

  it("rejects and cancels an oversized stream before parsing", async () => {
    let canceled = false;
    const response = new Response(new ReadableStream({
      start(controller) { controller.enqueue(new Uint8Array(9)); },
      cancel() { canceled = true; },
    }), { headers: { "Content-Type": "application/json" } });
    await expect(readBoundedWebJSON(response, 8)).rejects.toThrow(/size limit/);
    expect(canceled).toBe(true);
  });

  it("rejects an oversized declared length without reading its body", async () => {
    let canceled = false;
    const response = new Response(new ReadableStream({ cancel() { canceled = true; } }), {
      headers: { "Content-Type": "application/json", "Content-Length": "9" },
    });
    await expect(readBoundedWebJSON(response, 8)).rejects.toThrow(/size limit/);
    expect(canceled).toBe(true);
  });

  it("rejects malformed UTF-8, invalid JSON, wrong content type and absent bodies", async () => {
    await expect(readBoundedWebJSON(new Response(new Uint8Array([0xff]), { headers: { "Content-Type": "application/json" } }))).rejects.toThrow();
    await expect(readBoundedWebJSON(new Response("{", { headers: { "Content-Type": "application/json" } }))).rejects.toThrow();
    await expect(readBoundedWebJSON(new Response("{}", { headers: { "Content-Type": "application/jsonx" } }))).rejects.toThrow();
    await expect(readBoundedWebJSON(new Response(null, { status: 204, headers: { "Content-Type": "application/json" } }))).rejects.toThrow();
  });
});
