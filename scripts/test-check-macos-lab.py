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

    def test_missing_skipped_failed_or_foreign(self):
        for events in ([], self.evidence()[1:], self.evidence()[:-1],
                       self.evidence() + [dict(Package=checker.PACKAGE, Action="skip")],
                       self.evidence() + [dict(Package=checker.PACKAGE, Action="fail")],
                       [dict(Package="other", Action="pass")]):
            with self.subTest(events=events), self.assertRaises(ValueError):
                self.check(events)


if __name__ == "__main__":
    unittest.main()
