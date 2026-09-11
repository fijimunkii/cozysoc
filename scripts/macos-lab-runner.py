#!/usr/bin/env python3
"""Child-owning monitor copied into a private, disposable macOS lab directory."""
import os
from pathlib import Path
import subprocess
import sys
import time


def run_lab(work: Path, timeout: float = 95.0) -> int:
    """Run one fixed test binary; join it before returning on failure or abort."""
    if (work / "abort").exists():
        return 130
    env = dict(os.environ)
    env.update(COZYSOC_MACOS_LAB="1", COZYSOC_LAB_PEER=str(work / "peer"),
               TMPDIR="/private/tmp")
    command = [str(work / "lab.test"), "-test.v=test2json", "-test.count=1",
               "-test.timeout=90s", "-test.run=^TestMACOSGatewayLab$"]
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


def main() -> int:
    work = Path(__file__).resolve().parent
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
