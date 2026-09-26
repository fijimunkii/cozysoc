import { describe, expect, it } from "vitest";
import { historyFixture } from "./history-fixtures.test-helper";
import { resolverHistoryFixture } from "./resolver-history-fixtures.test-helper";
import { httpsHistoryFixture } from "./https-history-fixtures.test-helper";
import { gatewayHistoryExportJSON, resolverHistoryExportJSON, httpsHistoryExportJSON } from "./history-export";

describe("bounded quality history exports", () => {
  it.each([
    ["cozysoc-gateway-history", gatewayHistoryExportJSON, historyFixture],
    ["cozysoc-resolver-history", resolverHistoryExportJSON, resolverHistoryFixture],
    ["cozysoc-https-history", httpsHistoryExportJSON, httpsHistoryFixture],
  ])("serializes a versioned %s snapshot with original limits", (format, serialize, fixture) => {
    const snapshot = fixture();
    snapshot.truncated = true;
    snapshot.scan_truncated = true;
    const json = serialize(snapshot as never);
    expect(JSON.parse(json)).toEqual({ format, version: 1, snapshot });
    expect(json.endsWith("\n")).toBe(true);
  });

  it.each([
    [gatewayHistoryExportJSON, historyFixture],
    [resolverHistoryExportJSON, resolverHistoryFixture],
    [httpsHistoryExportJSON, httpsHistoryFixture],
  ])("rejects unexpected top-level and nested values before export", (serialize, fixture) => {
    const top = { ...fixture(), private_token: "do-not-export" };
    expect(() => serialize(top as never)).toThrow();
    const nested = fixture();
    Object.assign(nested.runs[0]!, { private_token: "do-not-export" });
    expect(() => serialize(nested as never)).toThrow();
  });
});
