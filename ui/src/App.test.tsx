import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { App } from "./App";
import { parseCoverageBundle } from "./coverage/bundle";
import { demoCoverageRaw } from "./demo/coverage";

const liveBundle = parseCoverageBundle({
  as_of: "2026-09-10T00:00:00Z",
  reports: [demoCoverageRaw],
});

describe("App live coverage boundary", () => {
  it("renders live controller coverage without demo labeling", async () => {
    render(<App loadCoverage={async () => liveBundle} />);
    expect(await screen.findByRole("status", { name: "Live controller data" })).toBeTruthy();
    expect(screen.queryByRole("status", { name: "Synthetic demo data" })).toBeNull();
    expect(screen.getByText("Device Watch")).toBeTruthy();
  });

  it("does not silently replace an unavailable controller with demo data", async () => {
    render(<App loadCoverage={async () => Promise.reject(new Error("offline"))} />);
    expect(await screen.findByRole("alert", { name: "Live monitoring unavailable" })).toBeTruthy();
    expect(screen.queryByRole("status", { name: "Synthetic demo data" })).toBeNull();
    expect(screen.queryByText("Device Watch")).toBeNull();
  });

  it("requires explicit user action before showing synthetic demo data", async () => {
    render(<App loadCoverage={async () => Promise.reject(new Error("offline"))} />);
    await screen.findByRole("alert", { name: "Live monitoring unavailable" });
    fireEvent.click(screen.getByRole("button", { name: "Use synthetic demo" }));
    const banner = screen.getByRole("status", { name: "Synthetic demo data" });
    expect(banner.textContent).toContain("This screen is not connected to live monitoring.");
    expect(screen.getByText("Device Watch")).toBeTruthy();
  });
});
