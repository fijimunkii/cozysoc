import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";

const csrf = "c".repeat(43);
const network = { interface_name: "en0", interface_index: 4, prefixes: ["192.168.1.0/24"] };

async function mockLiveSplit(page: Page) {
  const asOf = new Date().toISOString();
  const observedAt = new Date(Date.parse(asOf) - 60_000).toISOString();
  const validUntil = new Date(Date.parse(asOf) + 9 * 60_000).toISOString();
  const sourceDevice = { id: "device.source", user_label: "Speaker", first_seen: observedAt, last_seen: observedAt, state: "visible" };
  const targetDevice = { id: "device.split.one", first_seen: observedAt, last_seen: observedAt, state: "visible" };
  const source = { observation_id: "obs.neighbor.one", sensor_id: "sensor.dw", kind: "device-neighbor-seen", source_stream: "device-watch-neighbors", ingested_at: observedAt, attribution: "device-watch:arp-cache" };
  const evidence = [
    { kind: "mac", value: "02:00:00:00:00:01" },
    { kind: "ipv4", value: "192.168.1.42" },
  ].map((claim) => ({ ...claim, observed_at: observedAt, valid_until: validUntil, link_valid_until: validUntil, current: true, claim_confidence: .9, link_confidence: .9, authority: "inferred", reason: "device-watch:recent-mac-continuity", source_sensor_id: "sensor.dw", source }));
  const state = { split: false, splitRequests: [] as unknown[], undoRequests: [] as unknown[], csrfHeaders: [] as string[] };

  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const reply = (payload: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(payload) });
    switch (path) {
      case "/api/coverage": return reply({ as_of: asOf, reports: [] });
      case "/api/devices": return reply({ configured: true, scope_id: "scope.home", as_of: asOf, devices: [state.split ? targetDevice : sourceDevice], truncated: false });
      case "/api/networks": return reply({ candidates: [network], candidates_truncated: false, enrolled: { scope_id: "scope.home", enrolled_at: observedAt, interface: network } });
      case "/api/devices/merges": return reply({ configured: true, scope_id: "scope.home", merges: [] });
      case "/api/devices/splits": return reply({ configured: true, scope_id: "scope.home", splits: state.split ? [{ observation_id: source.observation_id, source_device_id: sourceDevice.id, target_device_id: targetDevice.id, created_at: asOf }] : [] });
      case "/api/devices/detail": return reply({ scope_id: "scope.home", as_of: asOf, truncated: false, device: sourceDevice, evidence });
      case "/api/session": return reply({ csrf_token: csrf });
      case "/api/devices/split":
        state.csrfHeaders.push(route.request().headers()["x-cozy-csrf"] ?? "");
        state.splitRequests.push(route.request().postDataJSON());
        state.split = true;
        return reply({ observation_id: source.observation_id, source_device_id: sourceDevice.id, target_device_id: targetDevice.id, changed: true });
      case "/api/devices/unsplit":
        state.csrfHeaders.push(route.request().headers()["x-cozy-csrf"] ?? "");
        state.undoRequests.push(route.request().postDataJSON());
        state.split = false;
        return reply({ observation_id: source.observation_id, changed: true });
      default: return reply({ error: "unavailable" }, 503);
    }
  });
  return state;
}

async function expectAccessible(page: Page) {
  const result = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(result.violations, JSON.stringify(result.violations, null, 2)).toEqual([]);
}

test("reviewed observation split and undo keep scope, provenance, focus, and narrow-screen access", async ({ page }) => {
  const state = await mockLiveSplit(page);
  await page.setViewportSize({ width: 320, height: 740 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/");
  await expect(page.getByRole("status", { name: "Live controller data" })).toBeVisible();
  await page.getByRole("button", { name: "Devices", exact: true }).click();
  await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });

  await page.getByLabel("Device with a wrong observation").selectOption("device.source");
  await page.getByLabel("Observation to separate").selectOption("obs.neighbor.one");
  await page.getByRole("button", { name: "Review split", exact: true }).press("Enter");
  await expect(page.getByRole("heading", { name: "Confirm observation split" })).toBeFocused();
  await expect(page.getByText("02:00:00:00:00:01", { exact: true })).toBeVisible();
  await expect(page.getByText("192.168.1.42", { exact: true })).toBeVisible();
  await expectAccessible(page);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(321);

  await page.getByRole("button", { name: "Cancel" }).press("Enter");
  await expect(page.getByRole("heading", { name: "Separate an observation" })).toBeFocused();
  await page.getByRole("button", { name: "Review split", exact: true }).press("Enter");
  await page.getByRole("button", { name: "Confirm split", exact: true }).press("Enter");
  await expect.poll(() => state.splitRequests).toEqual([{ source_device_id: "device.source", observation_id: "obs.neighbor.one" }]);
  await expect(page.getByRole("button", { name: "Review split undo" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Separate an observation" })).toBeFocused();

  await page.getByRole("button", { name: "Review split undo" }).press("Enter");
  await expect(page.getByRole("heading", { name: "Confirm split undo" })).toBeFocused();
  await expectAccessible(page);
  await page.getByRole("button", { name: "Confirm split undo" }).press("Enter");
  await expect.poll(() => state.undoRequests).toEqual([{ observation_id: "obs.neighbor.one" }]);
  await expect(page.getByText("No observations are currently separated.")).toBeVisible();
  expect(state.csrfHeaders).toEqual([csrf, csrf]);
});
