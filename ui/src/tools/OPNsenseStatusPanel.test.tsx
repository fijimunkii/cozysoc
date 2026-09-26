import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { OPNsenseStatusPanel } from "./OPNsenseStatusPanel";
import { parseOPNsenseStatus } from "./opnsense-status";

describe("OPNsenseStatusPanel", () => {
  it("reads only on request and links to the approved origin without credentials", async () => {
    const load = vi.fn().mockResolvedValue(parseOPNsenseStatus({ connected: true, endpoint: "https://192.168.1.1:8443", version: "26.7.4" }));
    render(<OPNsenseStatusPanel mode="live" load={load} />);
    expect(load).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Read OPNsense status" }));
    const link = await screen.findByRole("link", { name: "Open OPNsense admin UI" });
    expect(link.getAttribute("href")).toBe("https://192.168.1.1:8443");
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toContain("noreferrer");
    expect(screen.getByText(/does not prove network coverage/)).toBeTruthy();
    expect(load).toHaveBeenCalledTimes(1);
  });

  it("withholds a same-host link and never probes in demo mode", async () => {
    const load = vi.fn().mockResolvedValue(parseOPNsenseStatus({ connected: true, endpoint: "https://192.168.1.1", version: "26.7.4" }));
    const { rerender } = render(<OPNsenseStatusPanel mode="demo" load={load} webHostname="192.168.1.1" />);
    expect(screen.queryByRole("button", { name: "Read OPNsense status" })).toBeNull();
    expect(load).not.toHaveBeenCalled();
    rerender(<OPNsenseStatusPanel mode="live" load={load} webHostname="192.168.1.1" />);
    fireEvent.click(screen.getByRole("button", { name: "Read OPNsense status" }));
    await waitFor(() => expect(screen.getByText(/withholds the direct link/)).toBeTruthy());
    expect(screen.queryByRole("link", { name: "Open OPNsense admin UI" })).toBeNull();
  });

  it("rejects unsafe status payloads", () => {
    expect(() => parseOPNsenseStatus({ connected: true, endpoint: "https://key:secret@192.168.1.1", version: "26.7.4" })).toThrow();
    expect(() => parseOPNsenseStatus({ connected: true, endpoint: "http://192.168.1.1", version: "26.7.4" })).toThrow();
    expect(() => parseOPNsenseStatus({ connected: true, endpoint: "https://192.168.1.1", version: "26.7.3" })).toThrow();
  });
});
