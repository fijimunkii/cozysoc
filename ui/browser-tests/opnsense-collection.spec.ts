import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import { demoCapabilitiesRaw, demoStatusRaw } from "../src/demo/tools";

test("OPNsense neighbor collection needs a separate bounded browser review", async ({ page }) => {
  let statusReads = 0, reviews = 0, runs = 0;
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    const reply = (value: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
    switch (path) {
      case "/api/session": return reply({ csrf_token: "c".repeat(43) });
      case "/api/coverage": return reply({ as_of: new Date().toISOString(), reports: [] });
      case "/api/devices": return reply({ configured: false, as_of: new Date().toISOString(), devices: [], truncated: false });
      case "/api/status": return reply(demoStatusRaw);
      case "/api/capabilities": return reply(demoCapabilitiesRaw);
      case "/api/opnsense/status":
        statusReads++;
        return reply({ connected: true, endpoint: "https://192.168.50.1", version: "26.7.4" });
      case "/api/opnsense/collection/review":
        reviews++;
        expect(route.request().postDataJSON()).toEqual({});
        expect(route.request().headers()["x-cozy-csrf"]).toBe("c".repeat(43));
        return reply({ review_id: "r".repeat(43), expires_at: new Date(Date.now() + 5 * 60_000).toISOString(),
          endpoint: "https://192.168.50.1", scope_id: "scope.home", interface: { interface_name: "en0", interface_index: 7,
            prefixes: ["192.168.50.0/24"] }, max_rows_per_family: 256 });
      case "/api/opnsense/collection/run": {
        runs++;
        const body = route.request().postDataJSON();
        expect(body.review_id).toBe("r".repeat(43));
        expect(route.request().headers()["x-cozy-csrf"]).toBe("c".repeat(43));
        return reply(body.approve ? { outcome: "completed", result: { scope_id: "scope.home", read: 2,
          ipv4_total: 2, ipv6_total: 0, ipv4_truncated: false, ipv6_truncated: false,
          inserted: 1, deduplicated: 0, skipped_outside_scope: 1, skipped_duplicate: 0 } } : { outcome: "declined" });
      }
      default: return reply({ error: "unavailable" }, 503);
    }
  });
  await page.setViewportSize({ width: 320, height: 740 });
  await page.goto("/");
  await expect(page.getByRole("status", { name: "Live controller data" })).toBeVisible();
  await page.getByRole("button", { name: "Tools", exact: true }).click();
  expect(statusReads).toBe(0); expect(reviews).toBe(0); expect(runs).toBe(0);
  await page.getByRole("button", { name: "Read OPNsense status" }).click();
  await expect(page.getByRole("button", { name: "Review one router neighbor collection" })).toBeVisible();
  expect(statusReads).toBe(1); expect(reviews).toBe(0); expect(runs).toBe(0);
  await page.getByRole("button", { name: "Review one router neighbor collection" }).click();
  await expect(page.getByRole("button", { name: "Approve one read" })).toBeVisible();
  await expect(page.getByText(/192\.168\.50\.0\/24/)).toBeVisible();
  expect(reviews).toBe(1); expect(runs).toBe(0);
  await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });
  const widths = await page.evaluate(() => ({ viewport: window.innerWidth, content: document.documentElement.scrollWidth }));
  expect(widths.content).toBeLessThanOrEqual(widths.viewport + 1);
  const accessibility = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(accessibility.violations).toEqual([]);
  await page.getByRole("button", { name: "Decline" }).click();
  await expect(page.getByText("No router neighbor read was approved.")).toBeVisible();
  expect(runs).toBe(1);
  await page.getByRole("button", { name: "Review one router neighbor collection" }).click();
  await page.getByRole("button", { name: "Approve one read" }).click();
  await expect(page.getByText(/Read 2 router rows; saved 1/)).toBeVisible();
  expect(reviews).toBe(2); expect(runs).toBe(2);
});
