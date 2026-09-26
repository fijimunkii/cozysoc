import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import { readFile } from "node:fs/promises";

test("live activity history exports only the reviewed bounded window", async ({ page }) => {
  const asOf = new Date().toISOString();
  const since = new Date(Date.parse(asOf) - 24 * 60 * 60_000).toISOString();
  const observedAt = new Date(Date.parse(asOf) - 60_000).toISOString();
  const activity = {
    configured: true, scope_id: "scope.home", since, as_of: asOf, truncated: false, unexpected_private_value: "do-not-export",
    items: [{
      id: "obs.one", kind: "first-observed", at: observedAt, device_id: "device.one", user_label: "Speaker",
      address_family: "ipv4", address: "192.168.1.20", hardware_address: "02:00:00:00:00:01", unexpected_private_value: "do-not-export",
      source: { observation_id: "obs.one", sensor_id: "sensor.dw", kind: "device-neighbor-seen", source_stream: "device-watch-neighbors", ingested_at: observedAt, attribution: "device-watch:arp-cache", unexpected_private_value: "do-not-export" },
    }],
  };
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    const reply = (payload: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(payload) });
    switch (path) {
      case "/api/coverage": return reply({ as_of: asOf, reports: [] });
      case "/api/devices": return reply({ configured: true, scope_id: "scope.home", as_of: asOf, devices: [], truncated: false });
      case "/api/activity": return reply(activity);
      default: return reply({ error: "unavailable" }, 503);
    }
  });
  await page.setViewportSize({ width: 320, height: 740 });
  await page.goto("/");
  await expect(page.getByRole("status", { name: "Live controller data" })).toBeVisible();
  await page.getByRole("button", { name: "Activity", exact: true }).click();
  await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });
  await expect(page.getByRole("heading", { name: "Positive device activity" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Save reviewed JSON" })).toHaveCount(0);
  await page.getByRole("button", { name: "Review JSON before saving" }).click();
  const preview = page.getByLabel("Activity history export preview");
  await expect(preview).toContainText('"format": "cozysoc-device-activity"');
  await expect(preview).toContainText('"address": "192.168.1.20"');
  await expect(preview).not.toContainText("do-not-export");
  const violations = (await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze()).violations;
  expect(violations).toEqual([]);
  const widths = await page.evaluate(() => ({ viewport: window.innerWidth, content: document.documentElement.scrollWidth }));
  expect(widths.content).toBeLessThanOrEqual(widths.viewport + 1);
  const downloadReady = page.waitForEvent("download");
  await page.getByRole("button", { name: "Save reviewed JSON" }).click();
  const download = await downloadReady;
  expect(download.suggestedFilename()).toBe("cozysoc-device-activity.json");
  const saved = await readFile(await download.path(), "utf8");
  expect(saved).toBe(await preview.textContent());
  expect(saved).not.toContain("do-not-export");
});
