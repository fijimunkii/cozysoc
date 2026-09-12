#!/usr/bin/env python3
"""Remove only the journaled HTTPS fixture identity on disposable hosted macOS."""
import hashlib
from pathlib import Path
import plistlib
import ssl
import subprocess
import tempfile


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
    def security(*args):
        subprocess.run(["/usr/bin/security", *args], check=True, timeout=10)
    with tempfile.TemporaryDirectory(prefix="trust-cleanup-", dir=work) as directory:
        before, after, filtered = [Path(directory) / name for name in ("before.plist", "after.plist", "filtered.plist")]
        security("trust-settings-export", "-d", str(before))
        expected = without_certificate(plistlib.loads(before.read_bytes()), hashlib.sha1(der).hexdigest())
        filtered.write_bytes(plistlib.dumps(expected))
        security("trust-settings-import", "-d", str(filtered))
        security("trust-settings-export", "-d", str(after))
        if plistlib.loads(after.read_bytes()) != expected:
            raise ValueError("trust settings removal could not be verified")
    security("delete-certificate", "-Z", sha256, "/Library/Keychains/System.keychain")


if __name__ == "__main__":
    cleanup(Path(__file__).resolve().parent)
