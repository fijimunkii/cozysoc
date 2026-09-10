from pathlib import Path

config_path = Path("ui/playwright.config.ts")
config = config_path.read_text()
marker = '  testDir: "./browser",\n'
if marker not in config:
    raise SystemExit("missing Playwright testDir marker")
config_path.write_text(config.replace(marker, marker + '  testMatch: "**/*.pw.ts",\n', 1))

source = Path("ui/browser/accessibility.spec.ts")
target = Path("ui/browser/accessibility.pw.ts")
if not source.exists():
    raise SystemExit("missing staged browser spec")
source.rename(target)
