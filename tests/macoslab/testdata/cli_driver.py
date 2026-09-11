#!/usr/bin/env python3
"""Fixed isolated-lab PTY driver. Own/reap exactly one interactive CLI child.

This simulates terminal input; it does not attest an attentive human. No production
verification is bypassed. Stdin EOF from the owning Go test cancels this child.
"""
import errno
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import signal
import subprocess
import sys
import termios
import time

MODES = {"decline", "preloaded", "overlong", "expire", "interrupt", "approve",
         "redirected-input", "redirected-output", "eof"}
PROMPT = b"Approval (default: decline): "
TARGET = "192.168.250.1"


def main():
    if len(sys.argv) != 2 or sys.argv[1] not in MODES or os.geteuid() == 0:
        return 1
    mode = sys.argv[1]
    work = Path(__file__).resolve().parent
    (work / "cli-done").unlink(missing_ok=True)
    (work / "cli-started").write_text("1\n", encoding="utf-8")
    master = slave = None
    child = None
    status = 1
    output = bytearray()
    try:
        if (work / "abort").exists() or select.select([0], [], [], 0)[0]:
            raise RuntimeError("owner was lost before CLI launch")
        master, slave = pty.openpty()
        # Keep the controlling session owner alive through the post-exit mode
        # check. Darwin revokes a terminal when its session leader exits; making
        # the CLI itself that leader makes tcgetattr fail even after a safe exit.
        os.setsid()
        fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
        os.tcsetpgrp(slave, os.getpgrp())
        # Ctrl-C targets the foreground group. The monitor must keep joining its
        # child. Caught handlers reset on the child's exec; product signals stay
        # unchanged. HUP also must not interrupt the monitor's own final cleanup.
        signal.signal(signal.SIGINT, lambda *_: None)
        signal.signal(signal.SIGHUP, lambda *_: None)
        before = termios.tcgetattr(slave)
        if mode == "preloaded":
            os.write(master, ("check " + TARGET + "\n").encode("ascii"))
        child = subprocess.Popen(
            [str(work / "cozysoc"), "network-quality-check", "--state-dir",
             str(work / "controller-state"), TARGET],
            stdin=subprocess.DEVNULL if mode == "redirected-input" else slave,
            stdout=subprocess.DEVNULL if mode == "redirected-output" else slave,
            stderr=slave, close_fds=True)
        os.set_blocking(master, False)
        answered = False
        until = time.monotonic() + 40
        while True:
            if (work / "abort").exists() or time.monotonic() >= until:
                raise RuntimeError("CLI owner aborted or timed out")
            ready, _, _ = select.select([master, 0], [], [], 0.05)
            if 0 in ready:
                raise RuntimeError("CLI owner closed its lifetime pipe")
            chunk = b""
            if master in ready:
                try:
                    chunk = os.read(master, 4096)
                except OSError as error:
                    if error.errno not in (errno.EIO, errno.EAGAIN):
                        raise
                    chunk = b""
                output.extend(chunk)
                if len(output) > 32768:
                    raise RuntimeError("CLI output exceeded fixture bound")
            if PROMPT in output and not answered:
                answered = True
                if mode in ("decline", "preloaded"):
                    os.write(master, b"\n")
                elif mode == "approve":
                    os.write(master, ("check " + TARGET + "\n").encode("ascii"))
                elif mode == "overlong":
                    os.write(master, b"x" * 200 + b"\n")
                elif mode == "interrupt":
                    os.write(master, b"\x03")
                elif mode == "eof":
                    os.write(master, b"\x04")
                elif mode == "expire":
                    pass
                else:
                    raise RuntimeError("redirected CLI reached review")
            # Darwin may keep the PTY readable at EOF/EIO after the child exits.
            # Drain actual bytes, not endless readiness notifications.
            if child.poll() is not None and not chunk:
                break
        exit_code = child.wait(timeout=1)
        if termios.tcgetattr(slave) != before:
            raise RuntimeError("CLI changed terminal modes")
        text = output.decode("utf-8", errors="strict").replace("\r\n", "\n")
        if mode.startswith("redirected-"):
            if exit_code == 0 or answered or "terminal input and output" not in text:
                raise RuntimeError("noninteractive CLI did not fail before review")
        else:
            required = ["EXPERIMENTAL", "gateway role NOT verified", "Source: 192.168.250.2",
                        "Interface: feth42", "Enrolled prefixes:", "Review expires:",
                        "at most 3 requests", "1000 ms apart", "120 bytes", "60000 ms",
                        "Privacy:", "not raw packets", "default: decline"]
            if not answered or any(part not in text for part in required):
                raise RuntimeError("interactive disclosure missing")
            if mode == "approve":
                if exit_code != 0 or "Run state: completed" not in text or "Sample: complete\n" not in text or "Matched replies: 3; completed timeouts: 0" not in text:
                    raise RuntimeError("CLI did not publish completed measured result")
            elif mode in ("decline", "preloaded"):
                if exit_code != 0 or "Declined. This command did not authorize a check." not in text or "Run reference:" in text:
                    raise RuntimeError("default/type-ahead consent did not decline")
            elif exit_code == 0 or "no approval was submitted" not in text or "Run reference:" in text:
                raise RuntimeError("interrupted/invalid/expired prompt did not fail closed")
        status = 0
        print(json.dumps({"mode": mode, "exit_code": exit_code, "output": text}))
        return 0
    except Exception:
        print("Bounded CLI fixture transcript:\n" + output[-6000:].decode("utf-8", errors="replace"), file=sys.stderr)
        raise
    finally:
        if child is not None and child.poll() is None:
            child.terminate()
            try:
                child.wait(timeout=2)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait(timeout=2)
        if master is not None:
            os.close(master)
        if slave is not None:
            os.close(slave)
        (work / "cli-done").write_text(str(status) + "\n", encoding="utf-8")


if __name__ == "__main__":
    sys.exit(main())
