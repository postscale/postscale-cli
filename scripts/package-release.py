#!/usr/bin/env python3
"""Build the five release archives from a clean, committed CLI source tree."""

import gzip
import hashlib
import io
import os
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile
import time
import zipfile

ROOT = Path(__file__).resolve().parents[1]
PLATFORMS = ("darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64", "windows_amd64")
FILES = ("README.md", "LICENSE", "THIRD_PARTY_NOTICES.txt", "CHANGELOG.md", "examples/message.json")


def main():
    version = sys.argv[1] if len(sys.argv) == 2 else ""
    if not re.fullmatch(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)", version):
        raise SystemExit("usage: package-release.py MAJOR.MINOR.PATCH")
    source = (ROOT / "cmd/postscale/main.go").read_text()
    if f'var version = "{version}"' not in source:
        raise SystemExit("release version must match the source default used by go install")
    status = subprocess.check_output(
        ["git", "status", "--porcelain", "--untracked-files=normal", "--", "."], cwd=ROOT, text=True
    )
    if status.strip():
        raise SystemExit("commit CLI source changes before packaging a release")
    epoch = int(subprocess.check_output(["git", "log", "-1", "--format=%ct"], cwd=ROOT, text=True))
    destination = ROOT / "dist" / "release" / version
    if destination.exists():
        raise SystemExit(f"release output already exists: {destination}; use a fresh checkout")
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".package-", dir=destination.parent) as directory:
        stage = Path(directory)
        binaries, archives = stage / "bin", stage / "archives"
        archives.mkdir()
        subprocess.run(
            ["make", "cross-check", f"VERSION={version}", f"DIST_DIR={binaries}"], cwd=ROOT, check=True
        )
        for target in PLATFORMS:
            windows = target.startswith("windows_")
            binary_name = "postscale.exe" if windows else "postscale"
            binary = binaries / ("postscale-" + target.replace("_", "-") + (".exe" if windows else ""))
            entries = [(binary_name, binary, 0o755)] + [(name, ROOT / name, 0o644) for name in FILES]
            extension = ".zip" if windows else ".tar.gz"
            archive_path = archives / f"postscale_{version}_{target}{extension}"
            if windows:
                with zipfile.ZipFile(archive_path, "w", compression=zipfile.ZIP_DEFLATED) as archive:
                    for name, path, mode in entries:
                        info = zipfile.ZipInfo(name, time.gmtime(max(epoch, 315532800))[:6])
                        info.create_system = 3
                        info.external_attr = (0o100000 | mode) << 16
                        info.compress_type = zipfile.ZIP_DEFLATED
                        archive.writestr(info, path.read_bytes())
            else:
                with archive_path.open("wb") as raw:
                    with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=epoch) as compressed:
                        with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as archive:
                            for name, path, mode in entries:
                                data = path.read_bytes()
                                info = tarfile.TarInfo(name)
                                info.size, info.mode, info.mtime = len(data), mode, epoch
                                archive.addfile(info, io.BytesIO(data))
        checksums = "".join(
            f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n"
            for path in sorted(archives.iterdir())
        )
        (archives / "checksums.txt").write_text(checksums)
        os.rename(archives, destination)
    subprocess.run([sys.executable, str(ROOT / "scripts/check-release.py"), str(destination)], check=True)
    print(f"Release artifacts: {destination}")


if __name__ == "__main__":
    main()
