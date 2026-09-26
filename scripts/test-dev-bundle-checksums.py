#!/usr/bin/env python3
"""Regression checks for the unsigned developer bundle inventory."""

import pathlib
import subprocess
import sys
import tempfile
import unittest

CHECKER = pathlib.Path(__file__).with_name("dev-bundle-checksums.py")


class BundleChecksumTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name) / "bundle"
        (self.root / "ui" / "dist").mkdir(parents=True)
        (self.root / "cozysoc").write_bytes(b"executable")
        (self.root / "BUILD-INFO.txt").write_text("unsigned developer bundle\n")
        (self.root / "LICENSE.txt").write_text("first-party license\n")
        (self.root / "SBOM.json").write_text("{}\n")
        (self.root / "THIRD-PARTY-NOTICES.txt").write_text("notices\n")
        (self.root / "ui" / "dist" / "index.html").write_text("<html></html>\n")

    def run_checker(self, operation):
        return subprocess.run(
            [sys.executable, str(CHECKER), operation, str(self.root)],
            capture_output=True, text=True, check=False,
        )

    def test_create_and_verify(self):
        self.assertEqual(self.run_checker("create").returncode, 0)
        self.assertEqual(self.run_checker("verify").returncode, 0)
        self.assertNotEqual(self.run_checker("create").returncode, 0)

    def test_incomplete_bundle_cannot_get_a_manifest(self):
        (self.root / "ui" / "dist" / "index.html").unlink()
        self.assertNotEqual(self.run_checker("create").returncode, 0)
        self.assertFalse((self.root / "CHECKSUMS.json").exists())

    def test_changed_missing_and_extra_files_fail(self):
        self.assertEqual(self.run_checker("create").returncode, 0)
        executable = self.root / "cozysoc"
        executable.write_bytes(b"tampered")
        self.assertNotEqual(self.run_checker("verify").returncode, 0)
        executable.write_bytes(b"executable")
        executable.unlink()
        self.assertNotEqual(self.run_checker("verify").returncode, 0)
        executable.write_bytes(b"executable")
        (self.root / "extra").write_bytes(b"unexpected")
        self.assertNotEqual(self.run_checker("verify").returncode, 0)

    def test_corrupt_manifest_and_symlink_fail(self):
        self.assertEqual(self.run_checker("create").returncode, 0)
        manifest = self.root / "CHECKSUMS.json"
        manifest.write_text("{")
        self.assertNotEqual(self.run_checker("verify").returncode, 0)
        manifest.unlink()
        self.assertEqual(self.run_checker("create").returncode, 0)
        original = manifest.read_text()
        manifest.write_text(original.replace('"schema_version": 1', '"schema_version": 2'))
        self.assertNotEqual(self.run_checker("verify").returncode, 0)
        manifest.write_text(original)
        (self.root / "link").symlink_to(self.root / "cozysoc")
        self.assertNotEqual(self.run_checker("verify").returncode, 0)


if __name__ == "__main__":
    unittest.main()
