"""Fail closed if an isolated native test was missing, skipped, or failed."""
import json
import sys

EXPECTED = {"TestMACOSGatewayLab"} | {
    "TestMACOSGatewayLab/" + name
    for name in ("route", "reply", "silent", "wrong-nonce", "cancel", "source-loss", "shutdown")
}
PACKAGE = "github.com/fijimunkii/cozysoc/tests/macoslab"


def validate(lines):
    passed = set()
    package_passed = False
    for line in lines:
        event = json.loads(line)
        if event.get("Package") != PACKAGE:
            raise ValueError("unexpected package")
        if event.get("Action") in ("fail", "skip"):
            raise ValueError("lab was skipped or failed")
        if event.get("Action") == "pass":
            if "Test" in event:
                passed.add(event["Test"])
            else:
                package_passed = True
    if passed != EXPECTED or not package_passed:
        raise ValueError("missing required live test evidence")


if __name__ == "__main__":
    with open(sys.argv[1], encoding="utf-8") as results:
        validate(results)
    print("All required isolated macOS network cases executed and passed.")
