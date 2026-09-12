import importlib.util
from pathlib import Path
import unittest
import plistlib
import tempfile
import subprocess

spec = importlib.util.spec_from_file_location("trust", Path(__file__).with_name("macos-lab-trust.py"))
trust = importlib.util.module_from_spec(spec)
spec.loader.exec_module(trust)

class TrustCleanupTests(unittest.TestCase):
    def test_policy_restored_after_failed_removal_and_interruption(self):
        original = {"class": "rule", "rule": ["entitled", "authenticate-admin"], "modified": 1.0}
        for interrupted in (False, True):
            with self.subTest(interrupted=interrupted), tempfile.TemporaryDirectory() as directory:
                work = Path(directory)
                current = dict(original)
                if interrupted:
                    (work / "trust-authorization.plist").write_bytes(plistlib.dumps(original))
                    current = {"class": "rule", "rule": ["is-root"]}
                def security(*args, input=None):
                    nonlocal current
                    self.assertEqual(args[0], "authorizationdb")
                    self.assertEqual(args[2], trust.RIGHT)
                    if args[1] == "write":
                        if input is not None:
                            current = plistlib.loads(input)
                            current["modified"] = 2.0
                        else:
                            self.assertEqual(args[3], "is-root")
                            current = {"class": "rule", "rule": ["is-root"]}
                    return subprocess.CompletedProcess(args, 0, plistlib.dumps(current))
                with self.assertRaisesRegex(RuntimeError, "removal failed"):
                    with trust.root_removal_policy(work, security):
                        self.assertEqual(current["rule"], ["is-root"])
                        raise RuntimeError("removal failed")
                self.assertEqual(current["rule"], original["rule"])
                self.assertFalse((work / "trust-authorization.plist").exists())

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
