import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";

import { demoCapabilitiesRaw, demoStatusRaw } from "../src/demo/tools";
import { maxWebJSONBytes } from "../src/web-json";

const wcagTags = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"];

async function expectAccessibleNarrowState(page: Page) {
  const result = await new AxeBuilder({ page }).withTags(wcagTags).analyze();
  expect(result.violations, JSON.stringify(result.violations, null, 2)).toEqual([]);
  const widths = await page.evaluate(() => ({
    viewport: innerWidth,
    content: document.documentElement.scrollWidth,
    overflowing: Array.from(document.querySelectorAll("body *"))
      .filter((element) => element.getBoundingClientRect().right > innerWidth + 1)
      .slice(0, 5)
      .map((element) => `${element.tagName.toLowerCase()}.${element.className} (${Math.round(element.getBoundingClientRect().right)}px)`),
  }));
  expect(widths.content, JSON.stringify(widths)).toBeLessThanOrEqual(widths.viewport + 1);
}

test("live outage and stale refresh recover without substituting demo evidence", async ({ page }) => {
  const asOf = new Date().toISOString();
  let coverageReads = 0;
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    const reply = (value: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
    switch (path) {
      case "/api/session": return reply({ csrf_token: "c".repeat(43) });
      case "/api/coverage":
        coverageReads++;
        return coverageReads === 1 || coverageReads === 3
          ? reply({ error: "controller_unavailable" }, 503)
          : reply({ as_of: asOf, reports: [] });
      case "/api/devices": return reply({ configured: false, as_of: asOf, devices: [], truncated: false });
      case "/api/networks": return reply({ candidates: [], candidates_truncated: false });
      case "/api/activity": return reply({ configured: false, since: new Date(Date.now() - 86400000).toISOString(), as_of: asOf, items: [], truncated: false });
      case "/api/status": return reply(demoStatusRaw);
      case "/api/capabilities": return reply(demoCapabilitiesRaw);
      default: return reply({ error: "unavailable" }, 503);
    }
  });

  await page.setViewportSize({ width: 320, height: 740 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/");
  await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });
  await expect(page.getByRole("alert", { name: "Live monitoring unavailable" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Live data is unavailable" })).toBeVisible();
  await expect(page.getByRole("status", { name: "Synthetic demo data" })).toHaveCount(0);
  await expectAccessibleNarrowState(page);

  await page.getByRole("button", { name: "Retry live connection" }).press("Enter");
  await expect(page.getByRole("status", { name: "Live controller data" })).toBeVisible();
  await page.getByRole("button", { name: "Coverage", exact: true }).click();
  await expect(page.getByRole("heading", { name: "No coverage reports yet" })).toBeVisible();

  await page.getByRole("button", { name: "Refresh evidence" }).press("Enter");
  await expect(page.getByRole("alert", { name: "Live evidence out of date" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Waiting for fresh evidence" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "No coverage reports yet" })).toHaveCount(0);
  await expect(page.getByRole("status", { name: "Synthetic demo data" })).toHaveCount(0);
  await expectAccessibleNarrowState(page);

  await page.getByRole("button", { name: "Retry live read" }).press("Enter");
  await expect(page.getByRole("status", { name: "Live controller data" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "No coverage reports yet" })).toBeVisible();
  expect(coverageReads).toBe(4);
});

test("oversized live coverage JSON fails closed and a later bounded read recovers", async ({ page }) => {
  const asOf = new Date().toISOString();
  const oversized = JSON.stringify({ as_of: asOf, reports: [], private_value: `household-secret${"x".repeat(maxWebJSONBytes)}` });
  let coverageReads = 0;
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    const reply = (value: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
    switch (path) {
      case "/api/coverage":
        coverageReads++;
        return coverageReads === 1 ? route.fulfill({ status: 200, contentType: "application/json", body: oversized }) : reply({ as_of: asOf, reports: [] });
      case "/api/devices": return reply({ configured: false, as_of: asOf, devices: [], truncated: false });
      case "/api/networks": return reply({ candidates: [], candidates_truncated: false });
      case "/api/activity": return reply({ configured: false, since: new Date(Date.parse(asOf) - 86400000).toISOString(), as_of: asOf, items: [], truncated: false });
      default: return reply({ error: "unavailable" }, 503);
    }
  });
  await page.goto("/");
  await expect(page.getByRole("alert", { name: "Live monitoring unavailable" })).toBeVisible();
  await expect(page.locator("body")).not.toContainText("household-secret");
  await page.getByRole("button", { name: "Retry live connection" }).click();
  await expect(page.getByRole("status", { name: "Live controller data" })).toBeVisible();
  expect(coverageReads).toBe(2);
});
