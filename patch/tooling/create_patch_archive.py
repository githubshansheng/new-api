from __future__ import annotations

import argparse
import gzip
from pathlib import Path
import stat
import tarfile


SOURCE_DATE_EPOCH = 1784160000  # 2026-07-16 00:00:00 UTC
EXECUTABLE_PATHS = {
    "patchctl.sh",
    "assets/new-api.sh",
    "bin/linux-amd64/new-api",
    "bin/linux-amd64/patchdb",
    "bin/linux-arm64/new-api",
    "bin/linux-arm64/patchdb",
    "bin/windows-amd64/new-api.exe",
    "bin/windows-amd64/patchdb.exe",
    "bin/windows-arm64/new-api.exe",
    "bin/windows-arm64/patchdb.exe",
}


def archive_mode(relative_path: str, is_directory: bool) -> int:
    if is_directory or relative_path in EXECUTABLE_PATHS:
        return 0o755
    return 0o644


def add_entry(
    archive: tarfile.TarFile,
    package_dir: Path,
    source: Path,
    archive_root: str,
) -> None:
    relative = source.relative_to(package_dir).as_posix()
    archive_name = archive_root if relative == "." else f"{archive_root}/{relative}"
    file_stat = source.lstat()
    if stat.S_ISLNK(file_stat.st_mode):
        raise RuntimeError(f"symbolic links are forbidden: {source}")
    if not stat.S_ISREG(file_stat.st_mode) and not stat.S_ISDIR(file_stat.st_mode):
        raise RuntimeError(f"special files are forbidden: {source}")

    info = archive.gettarinfo(str(source), arcname=archive_name)
    info.uid = 0
    info.gid = 0
    info.uname = "root"
    info.gname = "root"
    info.mtime = SOURCE_DATE_EPOCH
    info.mode = archive_mode(relative, source.is_dir())
    if source.is_dir():
        archive.addfile(info)
        return
    with source.open("rb") as file_handle:
        archive.addfile(info, fileobj=file_handle)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--package-dir", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--archive-root", required=True)
    args = parser.parse_args()

    package_dir = Path(args.package_dir).resolve(strict=True)
    output = Path(args.output).resolve()
    if not package_dir.is_dir():
        raise RuntimeError(f"package directory is not a directory: {package_dir}")
    if output == package_dir or package_dir in output.parents:
        raise RuntimeError("archive output must be outside the package directory")

    entries = [package_dir]
    entries.extend(
        sorted(
            package_dir.rglob("*"),
            key=lambda path: path.relative_to(package_dir).as_posix(),
        )
    )
    output.parent.mkdir(parents=True, exist_ok=True)
    with output.open("wb") as raw_output:
        with gzip.GzipFile(
            filename="",
            mode="wb",
            fileobj=raw_output,
            mtime=SOURCE_DATE_EPOCH,
        ) as gzip_output:
            with tarfile.open(
                fileobj=gzip_output,
                mode="w",
                format=tarfile.USTAR_FORMAT,
            ) as archive:
                for entry in entries:
                    add_entry(archive, package_dir, entry, args.archive_root)


if __name__ == "__main__":
    main()
