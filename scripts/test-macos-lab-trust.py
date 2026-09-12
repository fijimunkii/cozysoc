import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("trust", Path(__file__).with_name("macos-lab-trust.py"))
trust = importlib.util.module_from_spec(spec)
spec.loader.exec_module(trust)

class TrustCleanupTests(unittest.TestCase):
    def test_removes_only_exact_identity(self):
        original = {"trustVersion": 1, "trustList": {"ABC": {"policy": "fixture"}, "DEF": {"policy": "existing"}}}
        result = trust.without_certificate(original, "abc")
        self.assertEqual(result, {"trustVersion": 1, "trustList": {"DEF": {"policy": "existing"}}})
        self.assertIn("ABC", original["trustList"])

    def test_missing_or_ambiguous_identity_fails(self):
        for value in ({}, {"trustList": []}, {"trustList": {}}, {"trustList": {"abc": {}, "ABC": {}}}):
            with self.subTest(value=value), self.assertRaises(ValueError):
                trust.without_certificate(value, "abc")

if __name__ == "__main__":
    unittest.main()
