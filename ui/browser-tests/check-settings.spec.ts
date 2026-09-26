import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import { demoCapabilitiesRaw, demoStatusRaw } from "../src/demo/tools";

test("browser saves and retires explicit DNS and HTTPS targets without starting checks", async ({ page }) => {
  const writes: string[] = [];
  let checkCalls = 0;
  const dnsID = `selection.${"a".repeat(32)}`, httpsID = `https-selection.${"b".repeat(32)}`;
  const dns = { endpoint: "192.168.50.53:53", name: "example.invalid.", family: "ipv4", transport: "udp", query_type: "A", expect: "answer", destination_scope: "exact-endpoint" };
  const https = { endpoint: "192.168.50.53:443", server_name: "private.example", request_target: "/check", family: "ipv4", method: "HEAD", expected_status: 204, destination_policy: "exact-endpoint" };
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
      case "/api/network-quality/resolver/selections/save":
        expect(route.request().postDataJSON()).toEqual(dns); expect(route.request().headers()["x-cozy-csrf"]).toBe("c".repeat(43));
        writes.push("dns-save"); return reply({ selection_id: dnsID, settings: dns });
      case "/api/network-quality/https/selections/save":
        expect(route.request().postDataJSON()).toEqual(https); expect(route.request().headers()["x-cozy-csrf"]).toBe("c".repeat(43));
        writes.push("https-save"); return reply({ selection_id: httpsID, settings: https });
      case "/api/network-quality/resolver/selections/retire":
        expect(route.request().postDataJSON()).toEqual({ selection_id: dnsID });
        writes.push("dns-retire"); return reply({ schema_version: 1, selection_id: dnsID, state: "retired" });
      case "/api/network-quality/https/selections/retire":
        expect(route.request().postDataJSON()).toEqual({ selection_id: httpsID });
        writes.push("https-retire"); return reply({ schema_version: 1, selection_id: httpsID, state: "retired" });
      default:
        if (path.endsWith("/review") || path.endsWith("/run")) checkCalls++;
        return reply({ error: "unavailable" }, 503);
    }
  });
  await page.setViewportSize({ width: 320, height: 740 });
  await page.goto("/");
  await expect(page.getByRole("status", { name: "Live controller data" })).toBeVisible();
  const dnsPanel = page.getByRole("region", { name: "Set up a DNS check target" });
  await dnsPanel.getByRole("textbox", { name: "Numeric resolver IP and port 53" }).fill(dns.endpoint);
  await dnsPanel.getByRole("textbox", { name: "Fully qualified query name" }).fill(dns.name);
  await dnsPanel.getByLabel("Address family").selectOption(dns.family);
  await dnsPanel.getByLabel("Transport").selectOption(dns.transport);
  await dnsPanel.getByLabel("Query type").selectOption(dns.query_type);
  await dnsPanel.getByLabel("Expected answer").selectOption(dns.expect);
  await dnsPanel.getByLabel("Destination policy").selectOption(dns.destination_scope);
  await dnsPanel.getByRole("button", { name: "Save DNS target" }).click();
  await expect(dnsPanel.getByText(/did not send a query or approve a check/)).toBeVisible();
  const httpsPanel = page.getByRole("region", { name: "Set up an HTTPS check target" });
  await httpsPanel.getByRole("textbox", { name: "Numeric server IP and port 443" }).fill(https.endpoint);
  await httpsPanel.getByRole("textbox", { name: "TLS server name and HTTP Host" }).fill(https.server_name);
  await httpsPanel.getByRole("textbox", { name: "Exact path and optional query" }).fill(https.request_target);
  await httpsPanel.getByLabel("Address family").selectOption(https.family);
  await httpsPanel.getByLabel("HTTP method").selectOption(https.method);
  await httpsPanel.getByRole("spinbutton", { name: "Expected final HTTP status" }).fill(String(https.expected_status));
  await httpsPanel.getByLabel("Destination policy").selectOption(https.destination_policy);
  await httpsPanel.getByRole("button", { name: "Save HTTPS target" }).click();
  await expect(httpsPanel.getByText(/did not send a request or approve a check/)).toBeVisible();
  expect(writes).toEqual(["dns-save", "https-save"]); expect(checkCalls).toBe(0);
  await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });
  const widths = await page.evaluate(() => ({ viewport: window.innerWidth, content: document.documentElement.scrollWidth }));
  expect(widths.content).toBeLessThanOrEqual(widths.viewport + 1);
  const a11y = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(a11y.violations).toEqual([]);
  await dnsPanel.getByRole("button", { name: "Review retirement" }).click();
  expect(writes).toEqual(["dns-save", "https-save"]);
  await dnsPanel.getByRole("button", { name: "Retire this DNS target" }).click();
  await httpsPanel.getByRole("button", { name: "Review retirement" }).click();
  await httpsPanel.getByRole("button", { name: "Retire this HTTPS target" }).click();
  expect(writes).toEqual(["dns-save", "https-save", "dns-retire", "https-retire"]); expect(checkCalls).toBe(0);
});
