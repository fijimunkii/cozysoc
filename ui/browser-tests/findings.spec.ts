import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import { demoCapabilitiesRaw, demoStatusRaw } from "../src/demo/tools";

test("informational arrivals load on demand and keep source expiry visible", async ({ page }) => {
  let reads = 0;
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    const reply = (value: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
    switch (path) {
      case "/api/session": return reply({ csrf_token: "c".repeat(43) });
      case "/api/coverage": return reply({ as_of: new Date().toISOString(), reports: [] });
      case "/api/devices": return reply({ configured: false, as_of: new Date().toISOString(), devices: [], truncated: false });
      case "/api/status": return reply(demoStatusRaw);
      case "/api/capabilities": return reply(demoCapabilitiesRaw);
      case "/api/findings/arrivals":
        reads++;
        return reply({ as_of: "2026-09-26T17:00:00Z", truncated: false,
          items: [{ id: "finding.one", scope_id: "scope.home", observed_at: "2026-09-26T16:00:00Z", recorded_at: "2026-09-26T16:01:00Z", evidence_observation_id: "obs.one", evidence_retained: false, payload: "private mac 02:00:00:00:00:01" }] });
      default: return reply({ error: "unavailable" }, 503);
    }
  });
  await page.setViewportSize({ width: 320, height: 740 });
  await page.goto("/");
  await expect(page.getByRole("status", { name: "Live controller data" })).toBeVisible();
  expect(reads).toBe(0);
  await page.getByRole("button", { name: "Findings", exact: true }).click();
  await expect(page.getByRole("heading", { name: "New network identity observed" })).toBeVisible();
  expect(reads).toBe(1);
  await expect(page.getByText(/source observation has expired or is unavailable/i)).toBeVisible();
  await expect(page.getByText(/not security verdicts or desktop notifications/i)).toBeVisible();
  await expect(page.locator("main")).not.toContainText("private mac");
  await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });
  const widths = await page.evaluate(() => ({ viewport: window.innerWidth, content: document.documentElement.scrollWidth }));
  expect(widths.content).toBeLessThanOrEqual(widths.viewport + 1);
  const result = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(result.violations).toEqual([]);
  await page.getByRole("button", { name: "Review activity" }).click();
  await expect(page.getByRole("heading", { name: "What changed on your visible network" })).toBeVisible();
});
