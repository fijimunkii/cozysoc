import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ResolverSettingsPanel } from "./ResolverSettingsPanel";
import { HTTPSSettingsPanel } from "./HTTPSSettingsPanel";
import type { ResolverSelection } from "./resolver-check";
import type { HTTPSSelection } from "./https-check";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

const resolver: ResolverSelection = { selection_id: `selection.${"a".repeat(32)}`, settings: {
  endpoint: "192.168.50.53:53", name: "example.invalid.", family: "ipv4", transport: "udp", query_type: "A", expect: "answer", destination_scope: "exact-endpoint",
} };
const https: HTTPSSelection = { selection_id: `https-selection.${"b".repeat(32)}`, settings: {
  endpoint: "192.168.50.53:443", server_name: "private.example", request_target: "/check", family: "ipv4", method: "HEAD", expected_status: 204, destination_policy: "exact-endpoint",
} };

function fillResolver() {
  fireEvent.change(screen.getByRole("textbox", { name: "Numeric resolver IP and port 53" }), { target: { value: resolver.settings.endpoint } });
  fireEvent.change(screen.getByRole("textbox", { name: "Fully qualified query name" }), { target: { value: resolver.settings.name } });
  fireEvent.change(screen.getByLabelText("Address family"), { target: { value: "ipv4" } });
  fireEvent.change(screen.getByLabelText("Transport"), { target: { value: "udp" } });
  fireEvent.change(screen.getByLabelText("Query type"), { target: { value: "A" } });
  fireEvent.change(screen.getByLabelText("Expected answer"), { target: { value: "answer" } });
  fireEvent.change(screen.getByLabelText("Destination policy"), { target: { value: "exact-endpoint" } });
}

function fillHTTPS() {
  fireEvent.change(screen.getByRole("textbox", { name: "Numeric server IP and port 443" }), { target: { value: https.settings.endpoint } });
  fireEvent.change(screen.getByRole("textbox", { name: "TLS server name and HTTP Host" }), { target: { value: https.settings.server_name } });
  fireEvent.change(screen.getByRole("textbox", { name: "Exact path and optional query" }), { target: { value: https.settings.request_target } });
  fireEvent.change(screen.getByLabelText("Address family"), { target: { value: "ipv4" } });
  fireEvent.change(screen.getByLabelText("HTTP method"), { target: { value: "HEAD" } });
  fireEvent.change(screen.getByRole("spinbutton", { name: "Expected final HTTP status" }), { target: { value: "204" } });
  fireEvent.change(screen.getByLabelText("Destination policy"), { target: { value: "exact-endpoint" } });
}

describe("browser saved-target settings", () => {
  it("saves DNS settings without a check and requires a second retirement review", async () => {
    const client = { saveResolverSettings: vi.fn(async () => resolver), retireResolverSettings: vi.fn(async () => {}) };
    render(<ResolverSettingsPanel client={client} loadSelections={async () => [resolver]} />);
    expect(client.saveResolverSettings).not.toHaveBeenCalled();
    fillResolver();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Save DNS target" })); });
    expect(client.saveResolverSettings).toHaveBeenCalledExactlyOnceWith(resolver.settings);
    expect(screen.getByText(/did not send a query or approve a check/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Review retirement" }));
    expect(client.retireResolverSettings).not.toHaveBeenCalled();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Retire this DNS target" })); });
    expect(client.retireResolverSettings).toHaveBeenCalledExactlyOnceWith(resolver.selection_id);
  });

  it("holds an uncertain DNS save until an explicit read", async () => {
    const client = { saveResolverSettings: vi.fn(async () => { throw new Error("private socket path"); }), retireResolverSettings: vi.fn(async () => {}) };
    const load = vi.fn(async () => [resolver]);
    render(<ResolverSettingsPanel client={client} loadSelections={load} />);
    fillResolver();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Save DNS target" })); });
    expect(screen.getByRole("alert").textContent).toContain("do not automatically retry");
    expect(screen.queryByText(/private socket path/)).toBeNull();
    expect((screen.getByRole("button", { name: "Save DNS target" }) as HTMLButtonElement).disabled).toBe(true);
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Load saved DNS targets" })); });
    expect(load).toHaveBeenCalledTimes(1);
    expect(client.saveResolverSettings).toHaveBeenCalledTimes(1);
  });

  it("saves an explicit HTTPS request and retires only after review", async () => {
    const client = { saveHTTPSSettings: vi.fn(async () => https), retireHTTPSSettings: vi.fn(async () => {}) };
    render(<HTTPSSettingsPanel client={client} loadSelections={async () => [https]} />);
    fillHTTPS();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Save HTTPS target" })); });
    expect(client.saveHTTPSSettings).toHaveBeenCalledExactlyOnceWith(https.settings);
    expect(screen.getByText(/did not send a request or approve a check/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Review retirement" }));
    expect(client.retireHTTPSSettings).not.toHaveBeenCalled();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Retire this HTTPS target" })); });
    expect(client.retireHTTPSSettings).toHaveBeenCalledExactlyOnceWith(https.selection_id);
  });
});
