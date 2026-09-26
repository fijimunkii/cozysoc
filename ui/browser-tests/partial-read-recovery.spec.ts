import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

import { demoCapabilitiesRaw, demoStatusRaw } from "../src/demo/tools";

test("partial Activity and storage failures announce gaps and recover with a fresh read", async ({ page }) => {
  const asOf = new Date().toISOString();
  const since = new Date(Date.parse(asOf) - 86400000).toISOString();
  let activityReads = 0;
  let storageReads = 0;
  let statusReads = 0;
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    const reply = (value: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
    switch (path) {
      case "/api/session": return reply({ csrf_token: "c".repeat(43) });
      case "/api/coverage": return reply({ as_of: asOf, reports: [] });
      case "/api/devices": return reply({ configured: false, as_of: asOf, devices: [], truncated: false });
      case "/api/networks": return reply({ candidates: [], candidates_truncated: false });
      case "/api/activity":
        activityReads++;
        return activityReads === 1 ? reply({ error: "unavailable" }, 503) : reply({ configured: false, since, as_of: asOf, items: [], truncated: false });
      case "/api/status":
        statusReads++;
        return statusReads === 3 ? reply({ error: "unavailable" }, 503) : reply(demoStatusRaw);
      case "/api/capabilities": return reply(demoCapabilitiesRaw);
      case "/api/storage":
        storageReads++;
        return storageReads === 1 ? reply({ error: "unavailable" }, 503) : reply({
          as_of: asOf, quota_state: "current", database_bytes: 1048576, used_bytes: 1048576, reusable_bytes: 0, max_bytes: 31457280,
          filesystem_state: "current", filesystem_supported: true, filesystem_total_bytes: 1073741824, filesystem_available_bytes: 536870912,
          retention: [{ class: "ephemeral", duration_seconds: 86400 }, { class: "short", duration_seconds: 604800 },
            { class: "standard", duration_seconds: 2592000 }, { class: "audit", duration_seconds: 15552000 }],
          inventory: { batch_evidence_records: 0, other_observations: 0, identity_claims: 0, coverage_samples: 0, findings: 0, audit_events: 0, saved_check_selections: 0, labeled_devices: 0 },
        });
      default: return reply({ error: "unavailable" }, 503);
    }
  });
  await page.setViewportSize({ width: 320, height: 740 });
  await page.goto("/");
  await expect(page.getByRole("status", { name: "Live controller data" })).toBeVisible();
  await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });

  await page.getByRole("button", { name: "Activity", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Activity is temporarily unavailable" })).toBeVisible();
  await expect(page.getByRole("alert")).toBeVisible();
  await expect(page.getByRole("button", { name: "Retry activity" })).toBeVisible();
  await page.getByRole("button", { name: "Tools", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Storage and retention" })).toBeVisible();
  await expect(page.getByRole("alert")).toContainText("Storage information is temporarily unavailable.");
  const a11y = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(a11y.violations).toEqual([]);
  const widths = await page.evaluate(() => ({ viewport: innerWidth, content: document.documentElement.scrollWidth }));
  expect(widths.content).toBeLessThanOrEqual(widths.viewport + 1);
  await page.getByRole("button", { name: "Retry storage read" }).press("Enter");
  await expect.poll(() => storageReads).toBe(2);
  await expect(page.getByText("30 MiB · Within quota")).toBeVisible();
  await expect(page.getByRole("button", { name: "Retry storage read" })).toHaveCount(0);
  await page.getByRole("button", { name: "Activity", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Device activity is not configured" })).toBeVisible();
  expect(activityReads).toBe(2);
  await page.getByRole("button", { name: "Tools", exact: true }).click();
  await page.getByRole("button", { name: "Refresh evidence" }).click();
  await expect(page.getByRole("heading", { name: "Tool information is temporarily unavailable" })).toBeVisible();
  await expect(page.getByRole("alert")).toBeVisible();
  await page.getByRole("button", { name: "Retry tools" }).press("Enter");
  await expect(page.getByRole("heading", { name: "Storage and retention" })).toBeVisible();
  expect(statusReads).toBe(4);
});
