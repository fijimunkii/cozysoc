import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

import { demoCapabilitiesRaw, demoStatusRaw } from "../src/demo/tools";

test("a failed device read leaves coverage live and pauses setup", async ({ page }) => {
  let deviceReads = 0;
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    const reply = (value: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
    switch (path) {
      case "/api/session": return reply({ csrf_token: "c".repeat(43) });
      case "/api/coverage": return reply({ as_of: new Date().toISOString(), reports: [] });
      case "/api/devices": deviceReads++; return reply({ error: "controller_unavailable" }, 503);
      case "/api/networks": return reply({ candidates: [], candidates_truncated: false });
      case "/api/activity": return reply({ configured: false, since: new Date(Date.now() - 86400000).toISOString(), as_of: new Date().toISOString(), items: [], truncated: false });
      case "/api/status": return reply(demoStatusRaw);
      case "/api/capabilities": return reply(demoCapabilitiesRaw);
      default: return reply({ error: "unavailable" }, 503);
    }
  });
  await page.setViewportSize({ width: 320, height: 740 });
  await page.goto("/");
  await expect(page.getByRole("status", { name: "Live controller data" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Device status is unavailable" })).toBeVisible();
  await expect(page.getByText("Unavailable", { exact: true })).toBeVisible();
  await expect(page.getByText(/Current presence is unknown; coverage is shown separately/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Enable Device Watch" })).toHaveCount(0);
  await page.getByRole("button", { name: "Coverage", exact: true }).click();
  await expect(page.getByRole("heading", { name: "No coverage reports yet" })).toBeVisible();
  await page.getByRole("button", { name: "Devices", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Device evidence is temporarily unavailable" })).toBeVisible();
  await expect(page.getByText(/Current presence is unknown/)).toBeVisible();
  expect(deviceReads).toBe(1);
  await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });
  const widths = await page.evaluate(() => ({ viewport: innerWidth, content: document.documentElement.scrollWidth }));
  expect(widths.content).toBeLessThanOrEqual(widths.viewport + 1);
  const a11y = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(a11y.violations).toEqual([]);
  await page.getByRole("button", { name: "Retry device evidence" }).click();
  await expect.poll(() => deviceReads).toBe(2);
});
