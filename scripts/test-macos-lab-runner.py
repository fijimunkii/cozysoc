#!/usr/bin/env python3
"""Portable process ownership tests; no network or privileged operation."""
import importlib.util
import os
from pathlib import Path
import sys
import tempfile
import threading
import time
import unittest
from unittest import mock

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
                self.executable("import time\ntime.sleep(30)\n")
                children = []
                timer = threading.Timer(0.1, (self.work / "abort").touch)
                original_popen = runner.subprocess.Popen

                def track_child(*args, **kwargs):
                    child = original_popen(*args, **kwargs)
                    children.append(child)
                    # Revoke only after creation. Do not assume the child can
                    # finish Python startup/write a PID file within 400 ms.
                    if abort:
                        timer.start()
                    return child

                try:
                    with mock.patch.object(runner.subprocess, "Popen", side_effect=track_child):
                        result = runner.run_lab(self.work, timeout=5 if abort else 0.1)
                finally:
                    timer.cancel()
                    if abort and children:
                        timer.join()
                self.assertEqual(result, 130 if abort else 124)
                self.assertEqual(len(children), 1)
                # returncode is set only after Popen poll/wait has reaped it.
                self.assertIsNotNone(children[0].returncode)
                (self.work / "abort").unlink(missing_ok=True)


class ControllerMonitorTests(unittest.TestCase):
    setUp = RunnerTests.setUp
    def controller(self):
        path = self.work / "cozysoc"
        path.write_text(f"#!{sys.executable}\n" +
                        "import os, signal, sys, time\nfrom pathlib import Path\n" +
                        "signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))\n" +
                        "Path('controller-pid').write_text(str(os.getpid()))\n" +
                        "time.sleep(30)\n", encoding="utf-8")
        path.chmod(0o700)

    def test_controller_eof_and_timeout_join_child(self):
        for revoke in (True, False):
            with self.subTest(revoke=revoke):
                self.controller()
                (self.work / "controller-pid").unlink(missing_ok=True)
                read, write = os.pipe()
                def revoke_when_ready():
                    until = time.monotonic() + 4
                    while not (self.work / "controller-pid").exists() and time.monotonic() < until:
                        time.sleep(0.01)
                    os.close(write)
                timer = threading.Thread(target=revoke_when_ready)
                if revoke:
                    timer.start()
                try:
                    result = runner.run_controller(self.work, input_fd=read, timeout=5 if revoke else 2)
                    self.assertEqual(result, 0 if revoke else 124)
                    pid = int((self.work / "controller-pid").read_text())
                    with self.assertRaises(ProcessLookupError):
                        os.kill(pid, 0)
                    self.assertTrue((self.work / "controller-done").exists())
                finally:
                    if revoke:
                        timer.join()
                    else:
                        os.close(write)
                    os.close(read)

    def test_controller_delayed_start_after_owner_death(self):
        read, write = os.pipe()
        os.close(write)
        try:
            self.assertEqual(runner.run_controller(self.work, input_fd=read), 1)
            self.assertFalse((self.work / "controller.log").exists())
        finally:
            os.close(read)


if __name__ == "__main__":
    unittest.main()
