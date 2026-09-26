import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import { readFile } from "node:fs/promises";

test("live gateway history saves only the reviewed read without executing a check", async ({ page }) => {
  const asOf = new Date().toISOString().replace(/\.\d{3}Z$/, "Z");
  const since = new Date(Date.parse(asOf) - 24 * 60 * 60_000).toISOString().replace(/\.\d{3}Z$/, "Z");
  const history = { enrolled: true, as_of: asOf, since, truncated: true, scan_truncated: true, runs: [] };
  let reads = 0;
  const writes: string[] = [];
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    const reply = (payload: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(payload) });
    if (route.request().method() !== "GET") writes.push(path);
    switch (path) {
      case "/api/coverage": return reply({ as_of: asOf, reports: [] });
      case "/api/network-quality/history": reads++; return reply(history);
      default: return reply({ error: "unavailable" }, 503);
    }
  });
  await page.setViewportSize({ width: 320, height: 740 });
  await page.goto("/");
  await expect(page.getByRole("status", { name: "Live controller data" })).toBeVisible();
  await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });
  expect(reads).toBe(0);
  await expect(page.getByRole("button", { name: "Review gateway history JSON" })).toHaveCount(0);
  await page.getByRole("button", { name: "Read gateway history" }).click();
  expect(reads).toBe(1);
  await page.getByRole("button", { name: "Review gateway history JSON" }).click();
  const preview = page.getByLabel("gateway history JSON preview");
  await expect(preview).toContainText('"format": "cozysoc-gateway-history"');
  await expect(preview).toContainText('"scan_truncated": true');
  const violations = (await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze()).violations;
  expect(violations).toEqual([]);
  const widths = await page.evaluate(() => ({ viewport: window.innerWidth, content: document.documentElement.scrollWidth }));
  expect(widths.content).toBeLessThanOrEqual(widths.viewport + 1);
  const downloadReady = page.waitForEvent("download");
  await page.getByRole("button", { name: "Save gateway history JSON" }).click();
  const download = await downloadReady;
  expect(download.suggestedFilename()).toBe("cozysoc-gateway-history.json");
  expect(await readFile(await download.path(), "utf8")).toBe(await preview.textContent());
  expect(reads).toBe(1);
  expect(writes).toEqual([]);
  await page.getByRole("button", { name: "Refresh history list" }).click();
  expect(reads).toBe(2);
  await expect(preview).toHaveCount(0);
});
