import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";

const network = { interface_name: "en0", interface_index: 4, prefixes: ["192.168.1.0/24"] };

async function mockLiveDevice(page: Page, failFirstDetail: boolean) {
  const asOf = new Date().toISOString();
  const firstSeen = new Date(Date.parse(asOf) - 60 * 60_000).toISOString();
  const lastSeen = new Date(Date.parse(asOf) - 60_000).toISOString();
  const validUntil = new Date(Date.parse(asOf) + 9 * 60_000).toISOString();
  const device = { id: "device.one", user_label: "Speaker", first_seen: firstSeen, last_seen: lastSeen, state: "visible" };
  const source = { observation_id: "obs.one", sensor_id: "sensor.dw", kind: "device-neighbor-seen", source_stream: "device-watch-neighbors", ingested_at: lastSeen, attribution: "device-watch:arp-cache" };
  const detail = {
    scope_id: "scope.home", as_of: asOf, truncated: false, device,
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
  await expect(page.getByText("Current identity evidence", { exact: true })).toBeVisible();
  await expect(page.getByText("Historical identity evidence", { exact: true })).toBeVisible();
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

async function expectNoHorizontalOverflow(page: Page) {
  const widths = await page.evaluate(() => ({ viewport: window.innerWidth, content: document.documentElement.scrollWidth }));
  expect(widths.content).toBeLessThanOrEqual(widths.viewport + 1);
}
