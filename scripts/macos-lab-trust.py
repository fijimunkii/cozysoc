#!/usr/bin/env python3
"""Revoke the journaled HTTPS fixture identity on disposable hosted macOS."""
import hashlib
from pathlib import Path
import plistlib
import ssl
import subprocess
import tempfile


def fixture_entry(settings, fingerprint):
    trust = settings.get("trustList")
    if not isinstance(trust, dict):
        raise ValueError("missing trust list")
    matches = [key for key in trust if isinstance(key, str) and key.lower() == fingerprint.lower()]
    if len(matches) != 1:
        raise ValueError("fixture trust entry is not unique")
    return matches[0], trust[matches[0]]


def verify_revocation(before, after, fingerprint):
    key, original = fixture_entry(before, fingerprint)
    after_key, revoked = fixture_entry(after, fingerprint)
    expected = dict(before)
    expected["trustList"] = dict(before["trustList"])
    expected["trustList"][key] = revoked
    if key != after_key or expected != after:
        raise ValueError("unrelated trust settings changed")
    settings = original.get("trustSettings")
    if not isinstance(settings, list) or len(settings) != 1:
        raise ValueError("unexpected fixture trust policy")
    denied = dict(settings[0])
    denied["kSecTrustSettingsResult"] = 3  # kSecTrustSettingsResultDeny
    if revoked.get("trustSettings") != [denied]:
        raise ValueError("fixture trust was not replaced by an exact deny policy")


def cleanup(work):
    if (work / "disposable-trust-allowed").read_text() != "github-hosted\n":
        raise ValueError("disposable runner marker required")
    der = ssl.PEM_cert_to_DER_cert((work / "https-trust.pem").read_text())
    sha256 = hashlib.sha256(der).hexdigest()
    if (work / "https-trust.sha256").read_text().strip() != sha256:
        raise ValueError("fixture certificate hash mismatch")
    fingerprint = hashlib.sha1(der).hexdigest()
    def security(*args):
        return subprocess.run(["/usr/bin/security", *args], stdout=subprocess.PIPE,
                              check=True, timeout=10)
    with tempfile.TemporaryDirectory(prefix="trust-cleanup-", dir=work) as directory:
        before, after = [Path(directory) / name for name in ("before.plist", "after.plist")]
        security("trust-settings-export", "-d", str(before))
        original = plistlib.loads(before.read_bytes())
        _, entry = fixture_entry(original, fingerprint)
        settings = entry.get("trustSettings")
        if not isinstance(settings, list) or len(settings) != 1:
            raise ValueError("unexpected fixture trust policy")
        name = settings[0].get("kSecTrustSettingsPolicyString")
        if not isinstance(name, str) or not name.startswith("cozysoc-") or not name.endswith(".invalid"):
            raise ValueError("unexpected fixture hostname")
        # Removing the last admin entry requires interactive authorization on
        # hosted macOS. Replace its exact SSL grant with deny instead. The deny
        # record persists until VM disposal; no authorization policy is changed.
        security("add-trusted-cert", "-d", "-r", "deny", "-p", "ssl", "-s", name,
                 "-k", "/Library/Keychains/System.keychain", str(work / "https-trust.pem"))
        security("trust-settings-export", "-d", str(after))
        verify_revocation(original, plistlib.loads(after.read_bytes()), fingerprint)
    security("delete-certificate", "-Z", sha256, "/Library/Keychains/System.keychain")


if __name__ == "__main__":
    cleanup(Path(__file__).resolve().parent)
