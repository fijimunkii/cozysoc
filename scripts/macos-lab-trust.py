#!/usr/bin/env python3
"""Remove only the journaled HTTPS fixture identity on disposable hosted macOS."""
import hashlib
from pathlib import Path
import plistlib
import ssl
import subprocess
import tempfile
from contextlib import contextmanager

RIGHT = "com.apple.trust-settings.admin"


def policy_content(data):
    # authd updates these bookkeeping timestamps when restoring a saved rule.
    return {key: value for key, value in plistlib.loads(data).items()
            if key not in ("created", "modified")}


@contextmanager
def root_removal_policy(work, security):
    journal = work / "trust-authorization.plist"
    def restore():
        saved = journal.read_bytes()
        security("authorizationdb", "write", RIGHT, input=saved)
        actual = security("authorizationdb", "read", RIGHT).stdout
        if policy_content(actual) != policy_content(saved):
            raise ValueError("original trust authorization policy was not restored")
        journal.unlink()
    # Recover an interrupted earlier invocation before taking another snapshot.
    if journal.exists():
        restore()
    original = security("authorizationdb", "read", RIGHT).stdout
    policy_content(original)
    with journal.open("xb") as output:
        output.write(original)
    try:
        security("authorizationdb", "write", RIGHT, "is-root")
        active = plistlib.loads(security("authorizationdb", "read", RIGHT).stdout)
        if active.get("class") != "rule" or active.get("rule") != ["is-root"]:
            raise ValueError("root-only removal policy could not be verified")
        yield
    finally:
        restore()


def without_certificate(settings, fingerprint):
    trust = settings.get("trustList")
    if not isinstance(trust, dict):
        raise ValueError("missing trust list")
    matches = [key for key in trust if isinstance(key, str) and key.lower() == fingerprint.lower()]
    if len(matches) != 1:
        raise ValueError("fixture trust entry is not unique")
    result = dict(settings)
    result["trustList"] = dict(trust)
    del result["trustList"][matches[0]]
    return result


def cleanup(work):
    if (work / "disposable-trust-allowed").read_text() != "github-hosted\n":
        raise ValueError("disposable runner marker required")
    der = ssl.PEM_cert_to_DER_cert((work / "https-trust.pem").read_text())
    sha256 = hashlib.sha256(der).hexdigest()
    if (work / "https-trust.sha256").read_text().strip() != sha256:
        raise ValueError("fixture certificate hash mismatch")
    def security(*args, input=None):
        return subprocess.run(["/usr/bin/security", *args], input=input,
                              stdout=subprocess.PIPE, check=True, timeout=10)
    with tempfile.TemporaryDirectory(prefix="trust-cleanup-", dir=work) as directory:
        before, after = [Path(directory) / name for name in ("before.plist", "after.plist")]
        security("trust-settings-export", "-d", str(before))
        expected = without_certificate(plistlib.loads(before.read_bytes()), hashlib.sha1(der).hexdigest())
        # Removing the final admin entry can request interactive authorization,
        # even though installing it succeeded as root. Never permit all users.
        with root_removal_policy(work, security):
            security("remove-trusted-cert", "-d", str(work / "https-trust.pem"))
        security("trust-settings-export", "-d", str(after))
        if plistlib.loads(after.read_bytes()) != expected:
            raise ValueError("trust settings removal could not be verified")
    security("delete-certificate", "-Z", sha256, "/Library/Keychains/System.keychain")


if __name__ == "__main__":
    cleanup(Path(__file__).resolve().parent)
