#!/usr/bin/env python3
"""Create or verify the unsigned developer bundle's file inventory."""

import hashlib
import json
import pathlib
import sys

MANIFEST = "CHECKSUMS.json"
MAX_MANIFEST_BYTES = 1024 * 1024
REQUIRED_FILES = {"cozysoc", "BUILD-INFO.txt", "ui/dist/index.html"}


def inventory(root: pathlib.Path) -> list[dict[str, object]]:
    if root.is_symlink() or not root.is_dir():
        raise ValueError("bundle root must be a regular directory")
    files = []
    pending = [root]
    while pending:
        directory = pending.pop()
        for path in directory.iterdir():
            if path.is_symlink():
                raise ValueError("bundle contains a symlink")
            if path.is_dir():
                pending.append(path)
            elif path.is_file():
                relative = path.relative_to(root).as_posix()
                if relative == MANIFEST:
                    continue
                digest = hashlib.sha256()
                size = 0
                with path.open("rb") as source:
                    while block := source.read(65536):
                        digest.update(block)
                        size += len(block)
                files.append({"path": relative, "size": size, "sha256": digest.hexdigest()})
            else:
                raise ValueError("bundle contains a non-file entry")
    files.sort(key=lambda item: item["path"])
    if not files or len(files) > 10000:
        raise ValueError("bundle file count is invalid")
    if not REQUIRED_FILES.issubset({item["path"] for item in files}):
        raise ValueError("bundle is missing required files")
    return files


def expected_manifest(root: pathlib.Path) -> dict[str, object]:
    return {"schema_version": 1, "algorithm": "sha256", "files": inventory(root)}


def create(root: pathlib.Path) -> None:
    manifest = root / MANIFEST
    if manifest.exists() or manifest.is_symlink():
        raise ValueError("checksum manifest already exists")
    encoded = (json.dumps(expected_manifest(root), indent=2) + "\n").encode("utf-8")
    if len(encoded) > MAX_MANIFEST_BYTES:
        raise ValueError("checksum manifest exceeds size limit")
    with manifest.open("xb") as destination:
        destination.write(encoded)


def verify(root: pathlib.Path) -> None:
    manifest = root / MANIFEST
    if manifest.is_symlink() or not manifest.is_file():
        raise ValueError("checksum manifest is missing or invalid")
    with manifest.open("rb") as source:
        encoded = source.read(MAX_MANIFEST_BYTES + 1)
    if len(encoded) > MAX_MANIFEST_BYTES:
        raise ValueError("checksum manifest exceeds size limit")
    try:
        recorded = json.loads(encoded)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ValueError("checksum manifest is corrupt") from error
    if recorded != expected_manifest(root):
        raise ValueError("bundle files do not match checksum manifest")


def main() -> int:
    if len(sys.argv) != 3 or sys.argv[1] not in ("create", "verify"):
        print(f"usage: {sys.argv[0]} create|verify BUNDLE_DIRECTORY", file=sys.stderr)
        return 2
    try:
        if sys.argv[1] == "create":
            create(pathlib.Path(sys.argv[2]))
        else:
            verify(pathlib.Path(sys.argv[2]))
    except (OSError, ValueError) as error:
        print(f"developer bundle checksum check failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
