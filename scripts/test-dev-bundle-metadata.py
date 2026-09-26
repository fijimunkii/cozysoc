#!/usr/bin/env python3
"""Regression checks for shipped dependency and notice selection."""

import importlib.util
import json
import pathlib
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name("dev-bundle-metadata.py")
SPEC = importlib.util.spec_from_file_location("bundle_metadata", SCRIPT)
metadata = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(metadata)


class BundleMetadataTests(unittest.TestCase):
    def test_npm_runtime_graph_excludes_build_tools(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            lock = root / "package-lock.json"
            lock.write_text(json.dumps({"lockfileVersion": 3, "packages": {
                "": {"dependencies": {"react": "1", "react-dom": "1"}, "devDependencies": {"vite": "1"}},
                "node_modules/react": {"version": "1"},
                "node_modules/react-dom": {"version": "1", "dependencies": {"scheduler": "1"}},
                "node_modules/scheduler": {"version": "1"},
                "node_modules/vite": {"version": "1"},
            }}))
            for name in ("react", "react-dom", "scheduler", "vite"):
                (root / "node_modules" / name).mkdir(parents=True)
            packages = metadata.npm_runtime_packages(lock, root / "node_modules")
            self.assertEqual([name for name, _, _ in packages], ["react", "react-dom", "scheduler"])

    def test_missing_runtime_package_and_license_fail_closed(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            lock = root / "package-lock.json"
            lock.write_text(json.dumps({"lockfileVersion": 3, "packages": {
                "": {"dependencies": {"react": "1"}},
                "node_modules/react": {"version": "1"},
            }}))
            with self.assertRaisesRegex(ValueError, "not installed"):
                metadata.npm_runtime_packages(lock, root / "node_modules")
            package = root / "node_modules" / "react"
            package.mkdir(parents=True)
            with self.assertRaisesRegex(ValueError, "no top-level license"):
                metadata.license_texts(package)
            (package / "LICENSE").write_text("MIT\n")
            (package / "PATENTS").write_text("Patent grant\n")
            self.assertEqual(metadata.license_texts(package), [("LICENSE", "MIT\n"), ("PATENTS", "Patent grant\n")])


if __name__ == "__main__":
    unittest.main()
