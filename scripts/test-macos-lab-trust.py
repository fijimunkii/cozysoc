import copy
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("trust", Path(__file__).with_name("macos-lab-trust.py"))
trust = importlib.util.module_from_spec(spec)
spec.loader.exec_module(trust)

class TrustCleanupTests(unittest.TestCase):
    def fixture(self):
        before = {"trustVersion": 1, "trustList": {
            "ABC": {"trustSettings": [{"kSecTrustSettingsResult": 1, "kSecTrustSettingsPolicyString": "cozysoc-test.invalid", "policy": b"ssl"}]},
            "DEF": {"existing": "unchanged"}}}
        after = copy.deepcopy(before)
        after["trustList"]["ABC"]["trustSettings"][0]["kSecTrustSettingsResult"] = 3
        return before, after

    def test_exact_revocation_and_repeat(self):
        before, after = self.fixture()
        trust.verify_revocation(before, after, "abc")
        trust.verify_revocation(after, after, "abc")
        self.assertEqual(before["trustList"]["ABC"]["trustSettings"][0]["kSecTrustSettingsResult"], 1)

    def test_unrelated_changes_and_incomplete_revocation_fail(self):
        for change in ("other", "result", "scope", "extra"):
            before, after = self.fixture()
            if change == "other":
                after["trustList"]["DEF"] = {}
            elif change == "result":
                after["trustList"]["ABC"]["trustSettings"][0]["kSecTrustSettingsResult"] = 1
            elif change == "scope":
                del after["trustList"]["ABC"]["trustSettings"][0]["policy"]
            else:
                after["trustList"]["ABC"]["trustSettings"].append({"kSecTrustSettingsResult": 1})
            with self.subTest(change=change), self.assertRaises(ValueError):
                trust.verify_revocation(before, after, "abc")

    def test_missing_or_ambiguous_identity_fails(self):
        for value in ({}, {"trustList": []}, {"trustList": {}}, {"trustList": {"abc": {}, "ABC": {}}}):
            with self.subTest(value=value), self.assertRaises(ValueError):
                trust.fixture_entry(value, "abc")

if __name__ == "__main__":
    unittest.main()
