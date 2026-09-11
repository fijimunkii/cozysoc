#!/usr/bin/env python3
"""Portable process ownership tests; no network or privileged operation."""
import importlib.util
import os
from pathlib import Path
import sys
import tempfile
import threading
import unittest

spec = importlib.util.spec_from_file_location(
    "lab_runner", Path(__file__).with_name("macos-lab-runner.py"))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class RunnerTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.work = Path(self.directory.name)

    def executable(self, body):
        path = self.work / "lab.test"
        path.write_text(f"#!{sys.executable}\n" + body, encoding="utf-8")
        path.chmod(0o700)

    def test_preserves_success_and_failure(self):
        for status in (0, 7):
            self.executable(f"import sys\nprint('fixture output')\nsys.exit({status})\n")
            self.assertEqual(runner.run_lab(self.work), status)
            self.assertIn("fixture output", (self.work / "output").read_text())

    def test_preexisting_abort_does_not_start_a_process(self):
        (self.work / "abort").touch()
        self.assertEqual(runner.run_lab(self.work), 130)
        self.assertFalse((self.work / "output").exists())

    def test_timeout_and_abort_join_the_owned_child(self):
        for abort in (False, True):
            with self.subTest(abort=abort):
                self.executable("import os, time\nfrom pathlib import Path\n"
                                "Path('child-pid').write_text(str(os.getpid()))\n"
                                "time.sleep(30)\n")
                timer = threading.Timer(0.4, (self.work / "abort").touch)
                if abort:
                    timer.start()
                try:
                    result = runner.run_lab(self.work, timeout=0.8)
                finally:
                    timer.cancel()
                    if abort:
                        timer.join()
                self.assertEqual(result, 130 if abort else 124)
                pid = int((self.work / "child-pid").read_text())
                with self.assertRaises(ProcessLookupError):
                    os.kill(pid, 0)
                (self.work / "abort").unlink(missing_ok=True)


if __name__ == "__main__":
    unittest.main()
