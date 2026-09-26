import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";

const network = { interface_name: "en0", interface_index: 4, prefixes: ["192.168.1.0/24"] };

async function mockLiveDevice(page: Page, failFirstDetail: boolean) {
  const asOf = new Date().toISOString();
  const firstSeen = new Date(Date.parse(asOf) - 60 * 60_000).toISOString();
  const lastSeen = new Date(Date.parse(asOf) - 60_000).toISOString();
  const validUntil = new Date(Date.parse(asOf) + 9 * 60_000).toISOString();
  const device = { id: "device.one", user_label: "Speaker", first_seen: firstSeen, last_seen: lastSeen, state: "visible" };
  const source = { observation_id: "obs.one", sensor_id: "sensor.dw", kind: "device-neighbor-seen", source_stream: "device-watch-neighbors", ingested_at: lastSeen, attribution: "device-watch:arp-cache" };
  const detail = {
    scope_id: "scope.home", as_of: asOf, truncated: false, device, unexpected_private_value: "do-not-export",
    evidence: [
      { kind: "mac", value: "02:00:00:00:00:01", observed_at: lastSeen, valid_until: validUntil, link_valid_until: validUntil, current: true, claim_confidence: .9, link_confidence: .9, authority: "inferred", reason: "device-watch:new-mac-candidate:mac", source_sensor_id: "sensor.dw", source },
      { kind: "ipv6", value: "2001:db8:1111:2222:3333:4444:5555:6666", observed_at: firstSeen, valid_until: lastSeen, current: false, authority: "inferred", reason: "device-watch:recent-mac-continuity:ip", source_sensor_id: "sensor.dw", source: { ...source, observation_id: "obs.two", ingested_at: firstSeen, attribution: "device-watch:ndp-cache" } },
    ],
  };
  let detailAttempts = 0;
  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const reply = (payload: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(payload) });
    switch (path) {
      case "/api/coverage": return reply({ as_of: asOf, reports: [] });
      case "/api/devices": return reply({ configured: true, scope_id: "scope.home", as_of: asOf, devices: [device], truncated: false });
      case "/api/networks": return reply({ candidates: [network], candidates_truncated: false, enrolled: { scope_id: "scope.home", enrolled_at: firstSeen, interface: network } });
      case "/api/devices/detail":
        detailAttempts += 1;
        return failFirstDetail && detailAttempts === 1 ? reply({ error: "controller_unavailable" }, 503) : reply(detail);
      default: return reply({ error: "unavailable" }, 503);
    }
  });
}

async function openDeviceList(page: Page) {
  await page.goto("/");
  await expect(page.getByRole("status", { name: "Live controller data" })).toBeVisible();
  await page.getByRole("button", { name: "Devices", exact: true }).click();
  await expect(page.getByRole("heading", { name: "What Cozy SOC has actually seen" })).toBeFocused();
}

async function checkPage(page: Page) {
  const result = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  const failures = result.violations.flatMap((violation) => violation.nodes.map((node) => `${violation.id}: ${node.target.join(", ")} (${node.any.map((check) => check.message).join("; ")})`));
  expect(failures).toEqual([]);
}

test("device evidence failure, retry, details, and Back preserve focus and accessible copy", async ({ page }) => {
  await mockLiveDevice(page, true);
  await openDeviceList(page);
  await checkPage(page);
  await page.getByRole("button", { name: "View evidence" }).press("Enter");
  await expect(page.getByRole("heading", { name: "Device evidence is unavailable" })).toBeFocused();
  await checkPage(page);
  await page.getByRole("button", { name: "Retry evidence" }).press("Enter");
  await expect(page.getByRole("heading", { name: "Speaker" })).toBeFocused();
  await expect(page.getByText("Current identity evidence at read", { exact: true })).toBeVisible();
  await expect(page.getByText("Historical identity evidence at read", { exact: true })).toBeVisible();
  await checkPage(page);
  await page.getByText("Technical provenance", { exact: true }).first().click();
  await checkPage(page);
  await page.getByRole("button", { name: "Back to devices" }).press("Enter");
  await expect(page.getByRole("heading", { name: "Visible network identities" })).toBeFocused();
});

test("device detail and expanded provenance reflow at 320 pixels with 200% text", async ({ page }) => {
  await mockLiveDevice(page, false);
  await page.setViewportSize({ width: 320, height: 740 });
  await openDeviceList(page);
  await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });
  await page.getByRole("button", { name: "View evidence" }).click();
  await expect(page.getByRole("heading", { name: "Speaker" })).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await page.getByText("Technical provenance", { exact: true }).first().click();
  await expectNoHorizontalOverflow(page);
  await checkPage(page);
});

test("device evidence export requires a preview and saves only the reviewed fields", async ({ page }) => {
  await mockLiveDevice(page, false);
  await page.setViewportSize({ width: 320, height: 740 });
  await openDeviceList(page);
  await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });
  await page.getByRole("button", { name: "View evidence" }).click();
  await expect(page.getByRole("button", { name: "Save reviewed JSON" })).toHaveCount(0);
  await page.getByRole("button", { name: "Review JSON before saving" }).click();
  const preview = page.getByLabel("Device evidence export preview");
  await expect(preview).toContainText('"format": "cozysoc-device-evidence"');
  await expect(preview).toContainText('"value": "02:00:00:00:00:01"');
  await expect(preview).not.toContainText("do-not-export");
  await checkPage(page);
  await expectNoHorizontalOverflow(page);
  const downloadReady = page.waitForEvent("download");
  await page.getByRole("button", { name: "Save reviewed JSON" }).click();
  const download = await downloadReady;
  expect(download.suggestedFilename()).toBe("cozysoc-device-evidence.json");
  const saved = await readFile(await download.path(), "utf8");
  expect(saved).toBe(await preview.textContent());
  expect(saved).not.toContain("do-not-export");
});

test("clearing a label requires an accessible review and keeps saved exports separate", async ({ page }) => {
  await mockLiveDevice(page, false);
  const requests: unknown[] = [];
  await page.route("**/api/session", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ csrf_token: "a".repeat(43) }) }));
  await page.route("**/api/devices/label", async (route) => {
    requests.push(route.request().postDataJSON());
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ device_id: "device.one", user_label: "", changed: true }) });
  });
  await page.setViewportSize({ width: 320, height: 740 });
  await openDeviceList(page);
  await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });
  await page.getByRole("button", { name: "Clear label" }).press("Enter");
  await expect(page.getByRole("group", { name: "Clear label review" })).toContainText("older audit entries may still contain past label text");
  await expect(page.getByRole("group", { name: "Clear label review" })).toBeFocused();
  expect(requests).toEqual([]);
  await checkPage(page);
  await expectNoHorizontalOverflow(page);
  await page.getByRole("button", { name: "Keep label" }).click();
  await expect(page.getByRole("button", { name: "Clear label" })).toBeFocused();
  expect(requests).toEqual([]);
  await page.getByRole("button", { name: "Clear label" }).click();
  await page.getByRole("button", { name: "Confirm clear label" }).click();
  await expect.poll(() => requests).toEqual([{ device_id: "device.one", label: "" }]);
  await expect(page.locator(".device-label-actions")).toBeFocused();
});

async function expectNoHorizontalOverflow(page: Page) {
  const widths = await page.evaluate(() => ({ viewport: window.innerWidth, content: document.documentElement.scrollWidth }));
  expect(widths.content).toBeLessThanOrEqual(widths.viewport + 1);
}
