import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import { readFile } from "node:fs/promises";

import { demoCapabilitiesRaw, demoStatusRaw } from "../src/demo/tools";
import { diagnosticFixture } from "../src/tools/diagnostics-fixtures.test-helper";

test("live diagnostics load only on request, stay redacted, and reflow", async ({ page }) => {
  let previewReads = 0;
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    const reply = (value: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
    switch (path) {
      case "/api/session": return reply({ csrf_token: "c".repeat(43) });
      case "/api/coverage": return reply({ as_of: new Date().toISOString(), reports: [] });
      case "/api/devices": return reply({ configured: false, as_of: new Date().toISOString(), devices: [], truncated: false });
      case "/api/status": return reply(demoStatusRaw);
      case "/api/capabilities": return reply(demoCapabilitiesRaw);
      case "/api/diagnostics/preview":
        previewReads++;
        return reply({ ...diagnosticFixture, raw_error: "private /Users/name/token=abc" });
      default: return reply({ error: "unavailable" }, 503);
    }
  });
  await page.setViewportSize({ width: 320, height: 740 });
  await page.goto("/");
  await expect(page.getByRole("status", { name: "Live controller data" })).toBeVisible();
  await page.getByRole("button", { name: "Tools", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Diagnostic preview" })).toBeVisible();
  expect(previewReads).toBe(0);
  await page.getByRole("button", { name: "Preview diagnostics" }).click();
  await expect(page.getByRole("button", { name: "Save preview as JSON" })).toBeVisible();
  expect(previewReads).toBe(1);
  await expect(page.getByLabel("Diagnostic bundle preview")).toContainText('"failure_category": "sensor"');
  await expect(page.getByLabel("Diagnostic bundle preview")).not.toContainText("private");
  const downloadReady = page.waitForEvent("download");
  await page.getByRole("button", { name: "Save preview as JSON" }).click();
  const download = await downloadReady;
  expect(download.suggestedFilename()).toBe("cozysoc-diagnostic-preview.json");
  const saved = await readFile(await download.path(), "utf8");
  expect(saved).toContain('"failure_category": "sensor"');
  expect(saved).not.toContain("private");
  await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });
  const widths = await page.evaluate(() => ({ viewport: window.innerWidth, content: document.documentElement.scrollWidth }));
  expect(widths.content).toBeLessThanOrEqual(widths.viewport + 1);
  const result = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(result.violations).toEqual([]);
});
