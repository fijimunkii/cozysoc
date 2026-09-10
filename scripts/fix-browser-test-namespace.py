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

test_text = target.read_text()
old_reason = '    reason: "No home network is authorized for Device Watch yet.",\n'
new_reason = '    reason: "network-not-enrolled",\n'
if old_reason not in test_text:
    raise SystemExit("missing fresh-install coverage reason marker")
target.write_text(test_text.replace(old_reason, new_reason, 1))
