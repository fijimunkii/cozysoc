from pathlib import Path
import json


def replace_once(path: str, old: str, new: str) -> None:
    p = Path(path)
    text = p.read_text()
    if old not in text:
        raise SystemExit(f"missing marker in {path}: {old[:160]!r}")
    p.write_text(text.replace(old, new, 1))


package_path = Path("ui/package.json")
package = json.loads(package_path.read_text())
package["scripts"]["test:browser"] = "playwright test"
package_path.write_text(json.dumps(package, indent=2) + "\n")

Path("ui/playwright.config.ts").write_text(r'''import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./browser",
  fullyParallel: true,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? 2 : undefined,
  reporter: process.env.CI ? "line" : "list",
  use: {
    baseURL: "http://127.0.0.1:4173",
    trace: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
  webServer: {
    command: "npx vite preview --host 127.0.0.1 --port 4173 --strictPort",
    url: "http://127.0.0.1:4173",
    reuseExistingServer: !process.env.CI,
    stdout: "pipe",
    stderr: "pipe",
  },
});
''')

Path("ui/browser").mkdir(parents=True, exist_ok=True)
Path("ui/browser/accessibility.spec.ts").write_text(r'''import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";

import { demoActivityRaw } from "../src/demo/activity";
import { demoCapabilitiesRaw, demoStatusRaw } from "../src/demo/tools";

const asOf = "2026-09-10T12:00:00Z";

const freshCoverage = {
  as_of: asOf,
  reports: [{
    capability_id: "device-watch",
    configured: false,
    state: "unconfigured",
    reason: "No home network is authorized for Device Watch yet.",
    observation_points: [],
    next_step: "Choose the home network this computer may observe.",
  }],
};

const freshDevices = {
  configured: false,
  as_of: asOf,
  devices: [],
  truncated: false,
};

const freshActivity = {
  ...demoActivityRaw,
  configured: false,
  scope_id: undefined,
  items: [],
  truncated: false,
};

const freshNetworks = {
  candidates: [
    { interface_name: "en0", interface_index: 7, prefixes: ["192.168.50.0/24", "2001:db8:50::/64"] },
    { interface_name: "en7", interface_index: 12, prefixes: ["10.10.0.0/24"] },
  ],
  candidates_truncated: false,
};

const freshCapabilities = {
  ...demoCapabilitiesRaw,
  capabilities: demoCapabilitiesRaw.capabilities.map((capability) => ({
    ...capability,
    configured: false,
    state: { desired: "disabled", process: "not-applicable", verification: "unverified" },
  })),
};

async function installFreshInstallApi(page: Page): Promise<void> {
  const responses: Record<string, unknown> = {
    "/api/coverage": freshCoverage,
    "/api/devices": freshDevices,
    "/api/activity": freshActivity,
    "/api/networks": freshNetworks,
    "/api/status": demoStatusRaw,
    "/api/capabilities": freshCapabilities,
  };

  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const response = responses[url.pathname];
    if (request.method() !== "GET" || response === undefined) {
      await route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ code: "not_found", message: "fixture route not available" }) });
      return;
    }
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(response) });
  });
}

async function openFreshInstall(page: Page): Promise<void> {
  await installFreshInstallApi(page);
  await page.goto("/");
  await expect(page.getByRole("status", { name: "Live controller data" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Choose the home network to authorize" })).toBeVisible();
}

const primaryViews = [
  { button: "Overview", heading: "Your home at a glance" },
  { button: "Devices", heading: "What Cozy SOC has actually seen" },
  { button: "Activity", heading: "What changed on your network" },
  { button: "Coverage", heading: "Know what is visible. Know what is not." },
  { button: "Tools", heading: "What Cozy SOC can run" },
] as const;

test.describe("browser accessibility gate", () => {
  test("has no detectable WCAG A/AA violations across primary views", async ({ page }) => {
    await openFreshInstall(page);

    for (const view of primaryViews) {
      await page.getByRole("button", { name: view.button, exact: true }).click();
      await expect(page.getByRole("heading", { level: 1, name: view.heading })).toBeVisible();
      const results = await new AxeBuilder({ page })
        .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
        .analyze();
      expect(results.violations, `${view.button} accessibility violations`).toEqual([]);
    }
  });

  test("primary navigation and enrollment review are keyboard operable", async ({ page }) => {
    await openFreshInstall(page);

    const overview = page.getByRole("button", { name: "Overview", exact: true });
    await overview.focus();
    for (const next of ["Devices", "Activity", "Coverage", "Tools"] as const) {
      await page.keyboard.press("Tab");
      await expect(page.getByRole("button", { name: next, exact: true })).toBeFocused();
    }
    await page.keyboard.press("Enter");
    await expect(page.getByRole("heading", { level: 1, name: "What Cozy SOC can run" })).toBeVisible();

    await page.getByRole("button", { name: "Overview", exact: true }).click();
    const en0 = page.getByRole("radio", { name: /en0/ });
    await en0.focus();
    await page.keyboard.press("Space");
    await expect(en0).toBeChecked();
    const review = page.getByRole("button", { name: "Review selection" });
    await review.focus();
    await page.keyboard.press("Enter");
    await expect(page.getByRole("heading", { name: "Authorize en0?" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Authorize this network" })).toBeVisible();
  });

  test("reflows at 320 CSS pixels without horizontal application scrolling", async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 800 });
    await openFreshInstall(page);

    for (const view of primaryViews) {
      await page.getByRole("button", { name: view.button, exact: true }).click();
      await expect(page.getByRole("heading", { level: 1, name: view.heading })).toBeVisible();
      expect(await horizontalOverflow(page), `${view.button} overflow at 320px`).toEqual([]);
    }
  });

  test("remains usable at 200 percent text size", async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 900 });
    await openFreshInstall(page);
    await page.addStyleTag({ content: "html { font-size: 200% !important; }" });

    for (const view of ["Overview", "Devices", "Activity", "Coverage", "Tools"] as const) {
      await page.getByRole("button", { name: view, exact: true }).click();
      await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
      expect(await horizontalOverflow(page), `${view} overflow at 200% text`).toEqual([]);
    }
  });

  test("honors reduced-motion preference through computed browser styles", async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await openFreshInstall(page);

    const computed = await page.evaluate(() => {
      const canary = document.createElement("div");
      canary.style.transitionDuration = "2s";
      canary.style.animationDuration = "2s";
      canary.style.animationIterationCount = "infinite";
      document.body.append(canary);
      const style = getComputedStyle(canary);
      return {
        transitionDuration: style.transitionDuration,
        animationDuration: style.animationDuration,
        animationIterationCount: style.animationIterationCount,
        scrollBehavior: style.scrollBehavior,
      };
    });

    expect(durationMilliseconds(computed.transitionDuration)).toBeLessThanOrEqual(0.01);
    expect(durationMilliseconds(computed.animationDuration)).toBeLessThanOrEqual(0.01);
    expect(computed.animationIterationCount).toBe("1");
    expect(computed.scrollBehavior).toBe("auto");
  });
});

async function horizontalOverflow(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const offenders: string[] = [];
    if (document.documentElement.scrollWidth > document.documentElement.clientWidth + 1) {
      offenders.push(`document:${document.documentElement.scrollWidth}>${document.documentElement.clientWidth}`);
    }
    for (const element of Array.from(document.querySelectorAll<HTMLElement>("body *"))) {
      if (element.clientWidth === 0 || element.scrollWidth <= element.clientWidth + 1) continue;
      const style = getComputedStyle(element);
      if (style.overflowX !== "auto" && style.overflowX !== "scroll") continue;
      offenders.push(`${element.tagName.toLowerCase()}${element.className ? `.${String(element.className).replace(/\\s+/g, ".")}` : ""}:${element.scrollWidth}>${element.clientWidth}`);
    }
    return offenders;
  });
}

function durationMilliseconds(value: string): number {
  const first = value.split(",", 1)[0]?.trim() ?? "";
  if (first.endsWith("ms")) return Number.parseFloat(first);
  if (first.endsWith("s")) return Number.parseFloat(first) * 1000;
  return Number.POSITIVE_INFINITY;
}
''')

# At narrow widths, wrap primary navigation rather than making it a horizontal scroller.
replace_once(
    "ui/src/app-shell.css",
    "  .primary-nav { display: flex; overflow-x: auto; }\n  .primary-nav button { width: auto; white-space: nowrap; }\n",
    "  .primary-nav { display: flex; flex-wrap: wrap; overflow-x: visible; }\n  .primary-nav button { width: auto; white-space: normal; }\n",
)

# Ignore browser-test output locally.
replace_once(
    ".gitignore",
    "node_modules/\n.vite/\n",
    "node_modules/\n.vite/\nplaywright-report/\ntest-results/\n",
)

# Developer documentation.
frontend = Path("docs/development/frontend.md")
text = frontend.read_text()
text += r'''

## Real-browser accessibility gate

The frontend has a Chromium-based browser gate in addition to jsdom/component tests. Run it after a production build with:

```sh
cd ui
npm run build
npx playwright install chromium
npm run test:browser
```

The Playwright suite renders a fixture-driven fresh-install journey in a real browser. It intercepts only the already-defined browser API reads with bounded synthetic responses; it does not enroll, probe, or mutate a CI network. The page remains in **live** presentation mode so first-time network enrollment controls are included in the accessibility checks, but the fixture is test infrastructure rather than evidence of hardware/network support.

The gate currently proves:

- primary navigation and the network-selection/review portion of onboarding are keyboard operable;
- axe-core reports no WCAG 2.x A/AA violations on Overview, Devices, Activity, Coverage, and Tools, including automated color-contrast and semantic checks;
- all primary views reflow at a 320 CSS-pixel viewport without application-level horizontal scrolling;
- the same views remain usable at 200% root text size without application-level horizontal scrolling; and
- the `prefers-reduced-motion: reduce` rule actually wins in computed Chromium styles, including transition duration, animation duration/iterations, and scroll behavior.

Automated browser checks are a release gate, not a substitute for assistive-technology or usability sessions. #13 still owns manual screen-reader/usability validation and any platform-specific accessibility evidence that automation cannot establish.
'''
frontend.write_text(text)
