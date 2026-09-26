import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";

import { demoCoverageRaw } from "../src/demo/coverage";

const candidate = { interface_name: "en0", interface_index: 4, prefixes: ["192.168.1.0/24"] };
const wcagTags = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"];

async function mockLiveSetup(page: Page) {
  const state = { enrolled: false, enabled: false, enrollmentRequests: [] as unknown[] };
  const asOf = new Date().toISOString();
  const coverageReport = structuredClone(demoCoverageRaw) as { observation_points: Array<{ window: {
    started_at: string; ended_at: string; fresh_until: string;
  } }> };
  for (const point of coverageReport.observation_points) {
    point.window.started_at = asOf;
    point.window.ended_at = asOf;
    point.window.fresh_until = new Date(Date.parse(asOf) + 3 * 60_000).toISOString();
  }

  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const reply = (payload: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(payload) });
    switch (path) {
      case "/api/session":
        return reply(route.request().method() === "GET" ? { csrf_token: "c".repeat(43) } : {});
      case "/api/coverage":
        return reply({ as_of: asOf, reports: state.enabled ? [coverageReport] : [] });
      case "/api/devices":
        return reply({ configured: state.enabled, ...(state.enabled ? { scope_id: "scope.home" } : {}), as_of: asOf, devices: [], truncated: false });
      case "/api/networks":
        return reply({ candidates: [candidate], candidates_truncated: false, ...(state.enrolled ? {
          enrolled: { scope_id: "scope.home", enrolled_at: asOf, interface: candidate },
        } : {}) });
      case "/api/networks/enroll":
        state.enrollmentRequests.push(route.request().postDataJSON());
        state.enrolled = true;
        return reply({ scope_id: "scope.home", enrolled_at: asOf, interface: candidate, changed: true });
      case "/api/device-watch/enable":
        state.enabled = true;
        return reply({ scope_id: "scope.home", changed: true, active: true, state: {
          desired: "enabled", process: "not-applicable", verification: "unverified",
        } });
      default:
        return reply({ error: "unavailable" }, 503);
    }
  });
  return state;
}

async function expectNoWCAGViolations(page: Page) {
  const result = await new AxeBuilder({ page }).withTags(wcagTags).analyze();
  expect(result.violations, JSON.stringify(result.violations, null, 2)).toEqual([]);
}

test("live setup keeps keyboard focus and accessible names through explicit authorization", async ({ page }) => {
  const state = await mockLiveSetup(page);
  await page.goto("/");
  await expect(page.getByRole("status", { name: "Live controller data" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Choose the home network to authorize" })).toBeVisible();
  await expectNoWCAGViolations(page);

  const network = page.getByRole("radio", { name: /en0/ });
  await network.focus();
  await page.keyboard.press("Space");
  await expect(network).toBeChecked();
  const review = page.getByRole("button", { name: "Review selection" });
  await review.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("heading", { name: "Authorize en0?" })).toBeFocused();
  await expectNoWCAGViolations(page);

  await page.getByRole("button", { name: "Back" }).press("Enter");
  await expect(review).toBeFocused();
  await review.press("Enter");
  await page.getByRole("button", { name: "Authorize this network" }).press("Enter");
  await expect(page.getByRole("heading", { name: "Enable Device Watch when you are ready" })).toBeFocused();
  expect(state.enrollmentRequests).toEqual([{ interface_name: "en0", expected: candidate }]);

  await page.getByRole("button", { name: "Enable Device Watch" }).press("Enter");
  await expect(page.getByRole("heading", { name: "Device Watch is reporting current evidence" })).toBeFocused();
  await expectNoWCAGViolations(page);
});

test("narrow, enlarged, reduced-motion layout keeps setup controls available", async ({ page }) => {
  await mockLiveSetup(page);
  await page.setViewportSize({ width: 320, height: 740 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Choose the home network to authorize" })).toBeVisible();
  await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });
  await expect(page.getByRole("radio", { name: /en0/ })).toBeVisible();
  await expect(page.getByRole("button", { name: "Review selection" })).toBeVisible();
  const transitionDuration = await page.getByRole("button", { name: "Review selection" }).evaluate((button) => getComputedStyle(button).transitionDuration);
  expect(transitionDuration.split(",").every((duration) => Number.parseFloat(duration) <= 0.001)).toBe(true);
  await expectNoHorizontalOverflow(page);
  await page.getByRole("radio", { name: /en0/ }).check();
  await page.getByRole("button", { name: "Review selection" }).click();
  await expect(page.getByRole("heading", { name: "Authorize en0?" })).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectNoWCAGViolations(page);
});

async function expectNoHorizontalOverflow(page: Page) {
  const widths = await page.evaluate(() => ({ viewport: window.innerWidth, content: document.documentElement.scrollWidth }));
  expect(widths.content).toBeLessThanOrEqual(widths.viewport + 1);
}
