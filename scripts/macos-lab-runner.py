#!/usr/bin/env python3
"""Child-owning monitor copied into a private, disposable macOS lab directory."""
import os
import select
from pathlib import Path
import subprocess
import sys
import time


def run_lab(work: Path, timeout: float = 220.0) -> int:
    """Run one fixed test binary; join it before returning on failure or abort."""
    if (work / "abort").exists():
        return 130
    env = dict(os.environ)
    env.update(COZYSOC_MACOS_LAB="1", COZYSOC_LAB_PEER=str(work / "peer"),
               COZYSOC_LAB_PYTHON=sys.executable, TMPDIR="/private/tmp")
    command = [str(work / "lab.test"), "-test.v=test2json", "-test.count=1",
               "-test.timeout=210s", "-test.run=^TestMACOSGatewayLab$"]
    with (work / "output").open("wb") as out, (work / "error").open("wb") as err:
        process = subprocess.Popen(command, cwd=work, env=env, stdout=out, stderr=err)
        try:
            deadline = time.monotonic() + timeout
            while True:
                result = process.poll()
                if result is not None:
                    return result if result >= 0 else 128 - result
                if (work / "abort").exists():
                    return 130
                if time.monotonic() >= deadline:
                    return 124
                time.sleep(0.05)
        finally:
            # Only this unreaped child is signaled; no PID-file or global pkill.
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=3)
            # The controller has a separate child-owning monitor. EOF from the
            # reaped lab process revokes its lifetime; never kill a PID from a file.
            until = time.monotonic() + 12
            while any((work / (name + "-started")).exists() and not (work / (name + "-done")).exists()
                      for name in ("controller", "cli")):
                if time.monotonic() >= until:
                    raise RuntimeError("controller or CLI monitor did not drain")
                time.sleep(0.05)


def run_controller(work: Path, input_fd: int = 0, timeout: float = 180.0) -> int:
    """Own one real controller; stdin EOF revokes its lifetime, including crashes."""
    (work / "controller-started").write_text("1\n", encoding="utf-8")
    status = 1
    process = None
    try:
        # A delayed launch after the lab died must not create a new controller.
        if (work / "abort").exists() or select.select([input_fd], [], [], 0)[0]:
            return 1
        with (work / "controller.log").open("wb") as log:
            process = subprocess.Popen(
                [str(work / "cozysoc"), "serve", "--experimental-gateway-checks", "--experimental-resolver-checks", "--experimental-https-checks",
                 "--state-dir", str(work / "controller-state")],
                stdin=subprocess.DEVNULL, stdout=log, stderr=log, cwd=work)
            until = time.monotonic() + timeout
            cause = 1
            while process.poll() is None:
                if (work / "abort").exists():
                    cause = 130
                    break
                if time.monotonic() >= until:
                    cause = 124
                    break
                if select.select([input_fd], [], [], 0.05)[0]:
                    cause = 0 if os.read(input_fd, 1) == b"" else 1
                    break
            if process.poll() is None:
                process.terminate()
                try:
                    status = process.wait(timeout=6)
                    if status == 0:
                        status = cause
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=3)
                    status = 1
            else:
                # Exiting before its owner requested shutdown is not success.
                status = 1
    finally:
        if process is not None and process.poll() is None:
            process.kill()
            process.wait(timeout=3)
        (work / "controller-done").write_text(f"{status}\n", encoding="utf-8")
    return status


def main() -> int:
    work = Path(__file__).resolve().parent
    if len(sys.argv) == 2 and sys.argv[1] == "controller":
        if os.geteuid() == 0:
            return 1
        return run_controller(work)
    status = 1
    (work / "started").write_text("1\n", encoding="utf-8")
    try:
        if os.geteuid() == 0:
            raise RuntimeError("lab tests must not run as root")
        status = run_lab(work)
    except Exception as error:
        # No environment, credentials, or arbitrary exception text is recorded.
        print(f"lab launcher failed: {type(error).__name__}", file=sys.stderr)
    finally:
        temporary = work / "exit-code.tmp"
        temporary.write_text(f"{status}\n", encoding="utf-8")
        temporary.replace(work / "exit-code")
    return status


if __name__ == "__main__":
    sys.exit(main())
