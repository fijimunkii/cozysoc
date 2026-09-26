import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";

const sections = [
  { button: "Overview", heading: "Your home at a glance" },
  { button: "Devices", heading: "What Cozy SOC has actually seen" },
  { button: "Activity", heading: "What changed on your visible network" },
  { button: "Coverage", heading: "Know what is visible. Know what is not." },
  { button: "Tools", heading: "What Cozy SOC can run" },
] as const;

async function openDemo(page: Page) {
  await page.route("**/api/coverage", (route) => route.fulfill({ status: 503, contentType: "application/json", body: '{"error":"controller_unavailable"}' }));
  await page.goto("/");
  await page.getByRole("button", { name: "Use synthetic demo" }).click();
  await expect(page.getByRole("status", { name: "Synthetic demo data" })).toBeVisible();
}

async function checkPage(page: Page, section: string) {
  const result = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  const failures = result.violations.flatMap((violation) => violation.nodes.map((node) => `${violation.id}: ${node.target.join(", ")} (${node.any.map((check) => check.message).join("; ")})`));
  expect(failures, section).toEqual([]);
}

test("primary sections keep focus and pass automated accessibility checks", async ({ page }) => {
  await openDemo(page);
  await checkPage(page, "Overview");
  for (const section of sections.slice(1)) {
    await page.getByRole("button", { name: section.button, exact: true }).press("Enter");
    await expect(page.getByRole("heading", { name: section.heading, exact: true })).toBeFocused();
    await expect(page.getByRole("status", { name: "Synthetic demo data" })).toBeVisible();
    await checkPage(page, section.button);
  }
  await page.getByRole("button", { name: "View devices" }).press("Enter");
  await expect(page.getByRole("heading", { name: sections[1].heading })).toBeFocused();
});

test("primary sections reflow at 320 pixels with 200% text", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 740 });
  await openDemo(page);
  await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });
  for (const section of sections) {
    await page.getByRole("button", { name: section.button, exact: true }).click();
    await expect(page.getByRole("heading", { name: section.heading, exact: true })).toBeVisible();
    const widths = await page.evaluate(() => ({ viewport: window.innerWidth, content: document.documentElement.scrollWidth }));
    expect(widths.content, section.button).toBeLessThanOrEqual(widths.viewport + 1);
  }
});
