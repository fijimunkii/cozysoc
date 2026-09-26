#!/usr/bin/env python3
"""Inventory shipped developer-bundle dependencies and copy their notices."""

import json
import pathlib
import subprocess
import sys
from urllib.parse import quote


def go_binary_modules(binary: pathlib.Path) -> list[tuple[str, str]]:
    output = subprocess.run(
        ["go", "version", "-m", str(binary)],
        check=True, capture_output=True, text=True,
    ).stdout
    modules = []
    for line in output.splitlines():
        fields = line.split("\t")
        if len(fields) >= 4 and fields[1] == "dep":
            if fields[2] == "" or fields[3] == "":
                raise ValueError("binary contains an incomplete Go module identity")
            modules.append((fields[2], fields[3]))
    if not modules:
        raise ValueError("binary contains no Go dependency metadata")
    return sorted(set(modules))


def go_module_directories() -> dict[tuple[str, str], pathlib.Path]:
    output = subprocess.run(
        ["go", "list", "-m", "-json", "all"],
        check=True, capture_output=True, text=True,
    ).stdout
    decoder = json.JSONDecoder()
    directories = {}
    index = 0
    while index < len(output):
        while index < len(output) and output[index].isspace():
            index += 1
        if index == len(output):
            break
        module, length = decoder.raw_decode(output[index:])
        index += length
        if module.get("Dir") and module.get("Version"):
            directories[(module["Path"], module["Version"])] = pathlib.Path(module["Dir"])
    return directories


def npm_runtime_packages(lockfile: pathlib.Path, node_modules: pathlib.Path) -> list[tuple[str, str, pathlib.Path]]:
    lock = json.loads(lockfile.read_text(encoding="utf-8"))
    packages = lock["packages"]
    if lock.get("lockfileVersion") != 3 or "" not in packages:
        raise ValueError("expected a version 3 npm lockfile")
    found = {}
    pending = [("", name) for name in packages[""].get("dependencies", {})]
    while pending:
        parent, name = pending.pop()
        parent_path = pathlib.PurePosixPath(parent)
        candidates = [f"{parent_path}/node_modules/{name}" if parent else f"node_modules/{name}"]
        while parent_path.parts:
            parent_path = parent_path.parent
            candidates.append(f"{parent_path}/node_modules/{name}" if str(parent_path) != "." else f"node_modules/{name}")
        package_path = next((candidate for candidate in candidates if candidate in packages), None)
        if package_path is None:
            raise ValueError(f"npm runtime dependency is missing from lockfile: {name}")
        if package_path in found:
            continue
        package = packages[package_path]
        version = package.get("version")
        if not isinstance(version, str) or not version:
            raise ValueError(f"npm runtime dependency has no version: {name}")
        directory = lockfile.parent / package_path
        if not directory.is_dir() or directory.is_symlink() or not directory.is_relative_to(node_modules.parent):
            raise ValueError(f"npm runtime dependency is not installed: {name}")
        found[package_path] = (name, version, directory)
        pending.extend((package_path, child) for child in package.get("dependencies", {}))
    return sorted(found.values(), key=lambda item: (item[0], item[1]))


def license_texts(directory: pathlib.Path) -> list[tuple[str, str]]:
    files = sorted(
        path for path in directory.iterdir()
        if path.is_file() and not path.is_symlink()
        and path.name.upper().startswith(("LICENSE", "COPYING", "NOTICE"))
    )
    if not files:
        raise ValueError(f"dependency has no top-level license or notice: {directory.name}")
    return [(path.name, path.read_text(encoding="utf-8")) for path in files]


def create(binary: pathlib.Path, lockfile: pathlib.Path, node_modules: pathlib.Path, bundle: pathlib.Path) -> None:
    if not binary.is_file() or not bundle.is_dir() or binary.parent != bundle:
        raise ValueError("expected the built executable inside the bundle")
    directories = go_module_directories()
    dependencies = []
    notices = ["Third-party notices for the unsigned Cozy SOC developer bundle.\n",
               "This lists Go modules embedded in the executable and production npm packages for the browser UI.\n"]
    for name, version in go_binary_modules(binary):
        directory = directories.get((name, version))
        if directory is None:
            raise ValueError(f"Go module source is unavailable: {name}@{version}")
        dependencies.append({"type": "library", "name": name, "version": version,
                             "purl": f"pkg:golang/{quote(name, safe='/')}@{quote(version)}"})
        for filename, content in license_texts(directory):
            notices.append(f"\n{'=' * 72}\nGo module: {name}@{version}\nSource file: {filename}\n{'=' * 72}\n{content.rstrip()}\n")
    for name, version, directory in npm_runtime_packages(lockfile, node_modules):
        dependencies.append({"type": "library", "name": name, "version": version,
                             "purl": f"pkg:npm/{quote(name, safe='/')}@{quote(version)}"})
        for filename, content in license_texts(directory):
            notices.append(f"\n{'=' * 72}\nnpm package: {name}@{version}\nSource file: {filename}\n{'=' * 72}\n{content.rstrip()}\n")
    sbom = {"bomFormat": "CycloneDX", "specVersion": "1.6", "version": 1,
            "metadata": {"component": {"type": "application", "name": "Cozy SOC unsigned developer bundle"}},
            "components": dependencies}
    (bundle / "SBOM.json").write_text(json.dumps(sbom, indent=2) + "\n", encoding="utf-8")
    (bundle / "THIRD-PARTY-NOTICES.txt").write_text("".join(notices), encoding="utf-8")


def main() -> int:
    if len(sys.argv) != 5:
        print(f"usage: {sys.argv[0]} BINARY PACKAGE_LOCK NODE_MODULES BUNDLE", file=sys.stderr)
        return 2
    try:
        create(*(pathlib.Path(value) for value in sys.argv[1:]))
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        print(f"developer bundle metadata failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
