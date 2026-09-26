import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { OPNsenseStatusPanel } from "./OPNsenseStatusPanel";
import { type OPNsenseCollectionClient } from "./opnsense-collection";
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

  it("requires an explicit review before one router read", async () => {
    const load = vi.fn().mockResolvedValue(parseOPNsenseStatus({ connected: true, endpoint: "https://192.168.50.1", version: "26.7.4" }));
    const review = { review_id: "r".repeat(43), expires_at: "2999-01-01T00:00:00Z", endpoint: "https://192.168.50.1", scope_id: "scope.home",
      interface: { interface_name: "en0", interface_index: 7, prefixes: ["192.168.50.0/24"] }, max_rows_per_family: 256 as const };
    const collection: OPNsenseCollectionClient = { review: vi.fn().mockResolvedValue(review), decide: vi.fn().mockResolvedValue({ outcome: "completed", result: {
      scope_id: "scope.home", read: 2, ipv4_total: 2, ipv6_total: 0, ipv4_truncated: false, ipv6_truncated: false,
      inserted: 1, deduplicated: 0, skipped_outside_scope: 1, skipped_duplicate: 0 } }) };
    render(<OPNsenseStatusPanel mode="live" load={load} collection={collection} />);
    fireEvent.click(screen.getByRole("button", { name: "Read OPNsense status" }));
    fireEvent.click(await screen.findByRole("button", { name: "Review one router neighbor collection" }));
    expect(await screen.findByText(/consider up to 256 rows from each/)).toBeTruthy();
    expect(screen.getByText("192.168.50.0/24")).toBeTruthy();
    expect(collection.decide).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Approve one read" }));
    await waitFor(() => expect(collection.decide).toHaveBeenCalledWith(review, true));
    expect(await screen.findByText(/Read 2 router rows/)).toBeTruthy();
  });
});
