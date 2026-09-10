import { describe, expect, it } from "vitest";
import { demoCapabilitiesRaw, demoStatusRaw } from "../demo/tools";
import { parseToolsSnapshot } from "./tools";

describe("parseToolsSnapshot", () => {
  it("parses the bounded Device Watch tool projection", () => {
    const tools = parseToolsSnapshot(demoStatusRaw, demoCapabilitiesRaw);
    expect(tools.status.transport).toBe("unix");
    expect(tools.capabilities).toHaveLength(1);
    expect(tools.capabilities[0]?.state.process).toBe("not-applicable");
    expect(tools.capabilities[0]?.resources.measurement).toBe("unmeasured");
  });

  it("rejects unsupported operating states and duplicate capability ids", () => {
    expect(() => parseToolsSnapshot(demoStatusRaw, {
      ...demoCapabilitiesRaw,
      capabilities: [{ ...demoCapabilitiesRaw.capabilities[0], state: { desired: "enabled", process: "magic", verification: "verified" } }],
    })).toThrow(/process state is unsupported/);
    expect(() => parseToolsSnapshot(demoStatusRaw, {
      ...demoCapabilitiesRaw,
      capabilities: [demoCapabilitiesRaw.capabilities[0], demoCapabilitiesRaw.capabilities[0]],
    })).toThrow(/duplicate capability id/);
  });

  it("rejects fake numeric budgets on an unmeasured resource profile", () => {
    expect(() => parseToolsSnapshot(demoStatusRaw, {
      ...demoCapabilitiesRaw,
      capabilities: [{ ...demoCapabilitiesRaw.capabilities[0], resources: { measurement: "unmeasured", profile: "desktop-base", max_ram_mib: 64 } }],
    })).toThrow(/unmeasured resources cannot declare measured limits/);
  });
});
