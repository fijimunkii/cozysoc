import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

import { demoCapabilitiesRaw, demoStatusRaw } from "../src/demo/tools";

test("gateway check needs a separate visible one-shot approval and reflows", async ({ page }) => {
  const decisions: unknown[] = [];
  let reviews = 0;
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    const reply = (value: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
    const now = new Date();
    switch (path) {
      case "/api/session": return reply({ csrf_token: "c".repeat(43) });
      case "/api/coverage": return reply({ as_of: now.toISOString(), reports: [] });
      case "/api/devices": return reply({ configured: false, as_of: now.toISOString(), devices: [], truncated: false });
      case "/api/status": return reply(demoStatusRaw);
      case "/api/capabilities": return reply(demoCapabilitiesRaw);
      case "/api/network-quality/gateway/review":
        reviews++;
        expect(route.request().postDataJSON()).toEqual({ target: "192.168.50.1" });
        expect(route.request().headers()["x-cozy-csrf"]).toBe("c".repeat(43));
        return reply({ review_id: "a".repeat(43), created_at: now.toISOString(), expires_at: new Date(now.getTime() + 30000).toISOString(),
          target: "192.168.50.1", source: "192.168.50.23", interface_name: "en0", interface_index: 4, prefixes: ["192.168.50.0/24"],
          budget: { max_attempts: 3, min_interval_ms: 1000, attempt_timeout_ms: 1000, total_timeout_ms: 5000,
            payload_bytes: 32, max_icmp_request_bytes: 120, max_concurrent_runs: 1, min_run_interval_ms: 60000 } });
      case "/api/network-quality/gateway/run":
        decisions.push(route.request().postDataJSON());
        return reply({ outcome: "blocked", run_id: "b".repeat(32), failure_code: "precondition_failed" });
      default: return reply({ error: "unavailable" }, 503);
    }
  });
  await page.setViewportSize({ width: 320, height: 740 });
  await page.goto("/");
  await expect(page.getByRole("status", { name: "Live controller data" })).toBeVisible();
  await page.getByLabel("Selected gateway address").fill("192.168.50.1");
  expect(reviews).toBe(0);
  await page.getByRole("button", { name: "Review one-shot check" }).click();
  await expect(page.getByLabel("Gateway check review")).toContainText("192.168.50.23 via en0");
  expect(decisions).toHaveLength(0);
  await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });
  const widths = await page.evaluate(() => ({ viewport: window.innerWidth, content: document.documentElement.scrollWidth }));
  expect(widths.content).toBeLessThanOrEqual(widths.viewport + 1);
  const a11y = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(a11y.violations).toEqual([]);
  await page.getByRole("button", { name: "Approve one check to 192.168.50.1" }).click();
  await expect(page.getByText(/Check outcome:/)).toBeVisible();
  expect(decisions).toEqual([{ review_id: "a".repeat(43), approve: true }]);
});
