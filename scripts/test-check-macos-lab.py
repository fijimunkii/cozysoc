import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("checker", Path(__file__).with_name("check-macos-lab.py"))
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)


class EvidenceGateTests(unittest.TestCase):
    def evidence(self):
        return [dict(Package=checker.PACKAGE, Test=n, Action="pass") for n in checker.EXPECTED] + [dict(Package=checker.PACKAGE, Action="pass")]

    def check(self, events):
        checker.validate(map(json.dumps, events))

    def test_pass(self):
        self.check(self.evidence())

    def test_resolver_route_is_required(self):
        name = "TestMACOSGatewayLab/resolver-route"
        self.assertIn(name, checker.EXPECTED)
        without_resolver = [e for e in self.evidence() if e.get("Test") != name]
        with self.assertRaises(ValueError):
            self.check(without_resolver)
        for action in ("skip", "fail"):
            with self.subTest(action=action), self.assertRaises(ValueError):
                self.check(without_resolver + [dict(Package=checker.PACKAGE, Test=name, Action=action)])

    def test_https_route_is_required(self):
        name = "TestMACOSGatewayLab/native-session/https-native-session"
        self.assertIn(name, checker.EXPECTED)
        without_https = [e for e in self.evidence() if e.get("Test") != name]
        with self.assertRaises(ValueError):
            self.check(without_https)
        for action in ("skip", "fail"):
            with self.subTest(action=action), self.assertRaises(ValueError):
                self.check(without_https + [dict(Package=checker.PACKAGE, Test=name, Action=action)])

    def test_dns_cases_are_required(self):
        for mode in ("dns-answer", "dns-nxdomain", "dns-silent", "dns-wrong-id", "dns-cancel", "dns-source-loss", "native-session/resolver-native-session", "native-session/https-native-review"):
            name = "TestMACOSGatewayLab/" + mode
            self.assertIn(name, checker.EXPECTED)
            with self.subTest(mode=mode), self.assertRaises(ValueError):
                self.check([e for e in self.evidence() if e.get("Test") != name])

    def test_missing_skipped_failed_or_foreign(self):
        for events in ([], self.evidence()[1:], self.evidence()[:-1],
                       self.evidence() + [dict(Package=checker.PACKAGE, Action="skip")],
                       self.evidence() + [dict(Package=checker.PACKAGE, Action="fail")],
                       [dict(Package="other", Action="pass")]):
            with self.subTest(events=events), self.assertRaises(ValueError):
                self.check(events)


if __name__ == "__main__":
    unittest.main()
