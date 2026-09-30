#!/usr/bin/env python3
"""Verify archive contents and checksums, then smoke-test the native binary offline."""

import hashlib
import json
import os
from pathlib import Path
import platform
import re
import subprocess
import sys
import tarfile
import tempfile
import zipfile

PLATFORMS = ("darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64", "windows_amd64")
DOCUMENTS = {"README.md", "LICENSE", "THIRD_PARTY_NOTICES.txt", "CHANGELOG.md", "examples/message.json"}


def main():
    if len(sys.argv) != 2:
        raise SystemExit("usage: check-release.py RELEASE_DIRECTORY")
    directory = Path(sys.argv[1]).resolve()
    entries = {}
    for line in (directory / "checksums.txt").read_text().splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  (postscale_([0-9]+\.[0-9]+\.[0-9]+)_([a-z0-9_]+)\.(?:tar\.gz|zip))", line)
        if not match or match[2] in entries:
            raise SystemExit("invalid checksum manifest")
        entries[match[2]] = (match[1], match[3], match[4])
    versions = {entry[1] for entry in entries.values()}
    if len(versions) != 1 or {entry[2] for entry in entries.values()} != set(PLATFORMS) or len(entries) != 5:
        raise SystemExit("expected exactly five platform archives for one version")
    if {path.name for path in directory.iterdir()} != set(entries) | {"checksums.txt"}:
        raise SystemExit("unexpected or missing release files")
    version = versions.pop()
    machine = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine().lower())
    native = f"{platform.system().lower()}_{machine}"
    native_files = None
    for name, (digest, _, target) in entries.items():
        path = directory / name
        if hashlib.sha256(path.read_bytes()).hexdigest() != digest:
            raise SystemExit(f"checksum mismatch: {name}")
        binary = "postscale.exe" if target.startswith("windows_") else "postscale"
        expected = DOCUMENTS | {binary}
        if name.endswith(".zip"):
            with zipfile.ZipFile(path) as archive:
                if len(archive.infolist()) != len(expected) or set(archive.namelist()) != expected:
                    raise SystemExit(f"unexpected archive contents: {name}")
                files = {member: archive.read(member) for member in expected}
        else:
            with tarfile.open(path, "r:gz") as archive:
                members = archive.getmembers()
                if len(members) != len(expected) or {member.name for member in members} != expected or not all(member.isfile() for member in members):
                    raise SystemExit(f"unexpected archive contents: {name}")
                if archive.getmember(binary).mode & 0o111 != 0o111:
                    raise SystemExit(f"binary is not executable: {name}")
                files = {member: archive.extractfile(member).read() for member in expected}
        if target == native:
            native_files = files
    if native_files is None:
        raise SystemExit(f"no native archive for {native}")
    with tempfile.TemporaryDirectory(prefix="postscale-release-check-") as temporary:
        root = Path(temporary)
        for name, content in native_files.items():
            path = root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(content)
        binary = root / ("postscale.exe" if native.startswith("windows_") else "postscale")
        binary.chmod(0o755)
        env = {key: value for key, value in os.environ.items() if not key.startswith("POSTSCALE_")}
        env["POSTSCALE_CONFIG_DIR"] = str(root / "config")
        def run(*args):
            return subprocess.check_output([str(binary), *args], env=env, text=True)
        if run("--version").strip() != f"postscale version {version}":
            raise SystemExit("packaged version does not match the release")
        if "Postscale CLI" not in run("--help"):
            raise SystemExit("packaged help is missing")
        result = json.loads(run("emails", "send", "--file", str(root / "examples/message.json"), "--dry-run", "--json"))
        if result["data"] != {"valid": True, "submitted": False, "validation": "local_only"}:
            raise SystemExit("packaged dry-run failed")
    print(f"Verified five archives, SHA-256 checksums, and native {native} version/help/dry-run ({version})")


if __name__ == "__main__":
    main()
