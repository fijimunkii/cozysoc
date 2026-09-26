import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AdGuardStatusPanel } from "./AdGuardStatusPanel";
import { parseAdGuardStatus } from "./adguard-status";

describe("AdGuardStatusPanel", () => {
  it("probes only on request and offers a deliberate credential-free admin link", async () => {
    const load = vi.fn().mockResolvedValue(parseAdGuardStatus({ connected: true, endpoint: "https://192.0.2.5:3000", version: "v0.107.79", running: true, protection_enabled: true, filtering_enabled: true, query_log_enabled: false, anonymized_clients: true }));
    render(<AdGuardStatusPanel mode="live" load={load} />);
    expect(load).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Read AdGuard Home status" }));
    await waitFor(() => expect(screen.getByRole("link", { name: "Open AdGuard Home admin UI" })).toBeTruthy());
    const link = screen.getByRole("link", { name: "Open AdGuard Home admin UI" });
    expect(link.getAttribute("href")).toBe("https://192.0.2.5:3000");
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toContain("noreferrer");
    expect(screen.getByText(/not household coverage/)).toBeTruthy();
    expect(load).toHaveBeenCalledTimes(1);
  });

  it("does not probe in demo mode", () => {
    const load = vi.fn();
    render(<AdGuardStatusPanel mode="demo" load={load} />);
    expect(screen.queryByRole("button", { name: "Read AdGuard Home status" })).toBeNull();
    expect(load).not.toHaveBeenCalled();
  });

  it("withholds a direct same-host admin link", async () => {
    const load = vi.fn().mockResolvedValue(parseAdGuardStatus({ connected: true, endpoint: "http://127.0.0.1:3000", version: "v0.107.79", running: true, protection_enabled: true, filtering_enabled: true, query_log_enabled: false, anonymized_clients: false }));
    render(<AdGuardStatusPanel mode="live" load={load} webHostname="127.0.0.1" />);
    fireEvent.click(screen.getByRole("button", { name: "Read AdGuard Home status" }));
    await waitFor(() => expect(screen.getByText(/withholds the direct link/)).toBeTruthy());
    expect(screen.queryByRole("link", { name: "Open AdGuard Home admin UI" })).toBeNull();
  });
});
