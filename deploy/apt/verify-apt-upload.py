#!/usr/bin/env python3
"""Verify a complete signed APT publication without generating metadata."""

import argparse
import gzip
import hashlib
import json
import lzma
from pathlib import Path
import re
import subprocess
import tempfile


def fail(message: str) -> None:
    raise SystemExit(f"verify-apt-upload: {message}")


def digest(path: Path, algorithm: str = "sha256") -> str:
    value = hashlib.new(algorithm)
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()


def safe_relative(value: str) -> Path:
    path = Path(value)
    if path.is_absolute() or not value or any(part in ("", ".", "..") for part in path.parts):
        fail(f"unsafe relative path: {value}")
    return path


def parse_control(data: str) -> list[dict[str, str]]:
    records = []
    for paragraph in data.strip().split("\n\n") if data.strip() else []:
        record: dict[str, str] = {}
        key = None
        for line in paragraph.splitlines():
            if line[:1].isspace() and key:
                record[key] += "\n" + line
            elif ":" in line:
                key, value = line.split(":", 1)
                record[key] = value.lstrip()
        records.append(record)
    return records


def make_keyring(public_key: Path, output: Path) -> None:
    result = subprocess.run(
        ["gpg", "--batch", "--yes", "--dearmor", "--output", str(output), str(public_key)],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    if result.returncode != 0:
        output.write_bytes(public_key.read_bytes())


def verify_detached(keyring: Path, signature: Path, payload: Path) -> None:
    result = subprocess.run(
        ["gpgv", "--keyring", str(keyring), str(signature), str(payload)],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    if result.returncode != 0:
        fail(f"signature verification failed: {payload}")


def verify_release(upload: Path, keyring: Path, suite_dir: Path) -> None:
    release = suite_dir / "Release"
    inrelease = suite_dir / "InRelease"
    detached = suite_dir / "Release.gpg"
    for path in (release, inrelease, detached):
        if not path.is_file():
            fail(f"suite is missing {path.name}: {suite_dir.name}")
    with tempfile.NamedTemporaryFile() as cleartext:
        result = subprocess.run(
            ["gpgv", "--keyring", str(keyring), "--output", cleartext.name, str(inrelease)],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        if result.returncode != 0 or Path(cleartext.name).read_bytes() != release.read_bytes():
            fail(f"InRelease verification failed: {suite_dir.name}")
    verify_detached(keyring, detached, release)
    release_text = release.read_text()
    not_automatic = "NotAutomatic: yes\n" in release_text
    blocks_automatic_upgrades = "ButAutomaticUpgrades: no\n" in release_text
    if suite_dir.name.endswith("-testing"):
        if not not_automatic or not blocks_automatic_upgrades:
            fail(f"unsafe testing upgrade policy: {suite_dir.name}")
    elif not_automatic or blocks_automatic_upgrades:
        fail(f"stable suite disables automatic upgrades: {suite_dir.name}")

    sha_section = release_text.split("\nSHA256:\n", 1)
    if len(sha_section) != 2:
        fail(f"Release has no SHA256 section: {suite_dir.name}")
    for line in sha_section[1].splitlines():
        if not line.startswith(" "):
            break
        parts = line.split()
        if len(parts) != 3:
            fail(f"invalid Release checksum entry: {line}")
        expected, expected_size, relative = parts
        target = suite_dir / safe_relative(relative)
        if not target.is_file() or target.stat().st_size != int(expected_size) or digest(target) != expected:
            fail(f"Release checksum mismatch: {suite_dir.name}/{relative}")


def verify_packages(upload: Path) -> None:
    identities: dict[tuple[str, str, str], str] = {}
    for packages in sorted(upload.glob("dists/*/*/binary-*/Packages")):
        plain = packages.read_bytes()
        gz_path = packages.with_name("Packages.gz")
        xz_path = packages.with_name("Packages.xz")
        if not gz_path.is_file() or gzip.decompress(gz_path.read_bytes()) != plain:
            fail(f"Packages.gz mismatch: {packages}")
        if not xz_path.is_file() or lzma.decompress(xz_path.read_bytes()) != plain:
            fail(f"Packages.xz mismatch: {packages}")
        component = packages.relative_to(upload / "dists").parts[1]
        for record in parse_control(plain.decode()):
            required = ("Package", "Version", "Architecture", "Filename", "Size", "SHA256")
            if any(not record.get(field) for field in required):
                fail(f"incomplete package record: {packages}")
            if record["Architecture"] not in ("arm64", "all"):
                fail(f"unsupported package architecture: {record['Architecture']}")
            relative = safe_relative(record["Filename"])
            if len(relative.parts) < 2 or relative.parts[0] != "pool" or relative.parts[1] != component:
                fail(f"package is indexed from the wrong component: {relative}")
            payload = upload / relative
            if not payload.is_file() or payload.stat().st_size != int(record["Size"]) or digest(payload) != record["SHA256"]:
                fail(f"package payload mismatch: {relative}")
            key = record["Package"], record["Version"], record["Architecture"]
            if key in identities and identities[key] != record["SHA256"]:
                fail(f"conflicting package identity: {'/'.join(key)}")
            identities[key] = record["SHA256"]


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("upload", type=Path)
    parser.add_argument("trusted_key", type=Path)
    parser.add_argument("current_repository", type=Path)
    args = parser.parse_args()
    upload = args.upload.resolve()
    trusted_key = args.trusted_key.resolve()
    current = args.current_repository.resolve()
    if upload == Path("/") or not upload.is_dir() or not trusted_key.is_file():
        fail("invalid upload or trusted key path")
    if any(path.is_symlink() for path in upload.rglob("*")):
        fail("upload must not contain symbolic links")

    publication_path = upload / "PUBLICATION.json"
    checksums = upload / "SHA256SUMS"
    signature = upload / "SHA256SUMS.asc"
    for path in (publication_path, checksums, signature):
        if not path.is_file():
            fail(f"missing required file: {path.name}")
    publication = json.loads(publication_path.read_text())
    if publication.get("schema_version") != 1 or publication.get("channel") not in ("testing", "stable"):
        fail("invalid publication schema or channel")
    for key in ("suite", "product", "component", "version"):
        if not re.fullmatch(r"[a-z0-9][a-z0-9.+-]*", str(publication.get(key, ""))):
            fail(f"unsafe publication {key}")
    expected_base = publication.get("expected_base_state_sha256")
    if expected_base != "none" and not re.fullmatch(r"[0-9a-f]{64}", str(expected_base)):
        fail("invalid expected base state hash")
    current_state = "none"
    if (current / "SHA256SUMS").is_file():
        current_state = digest(current / "SHA256SUMS")
    if current_state != expected_base:
        fail(f"repository state changed: expected {expected_base}, found {current_state}")

    with tempfile.TemporaryDirectory() as temporary:
        keyring = Path(temporary) / "trusted.gpg"
        make_keyring(trusted_key, keyring)
        verify_detached(keyring, signature, checksums)
        expected_files = set()
        for line in checksums.read_text().splitlines():
            parts = line.split(maxsplit=1)
            if len(parts) != 2 or not re.fullmatch(r"[0-9a-f]{64}", parts[0]):
                fail("invalid SHA256SUMS entry")
            relative_text = parts[1].lstrip(" *")
            relative = safe_relative(relative_text)
            target = upload / relative
            if not target.is_file() or digest(target) != parts[0]:
                fail(f"publication checksum mismatch: {relative}")
            expected_files.add(relative.as_posix())
        actual_files = {
            path.relative_to(upload).as_posix()
            for root in ("PUBLICATION.json", "catalog", "dists", "pool")
            for path in ([upload / root] if (upload / root).is_file() else (upload / root).rglob("*"))
            if path.is_file()
        }
        if actual_files != expected_files:
            fail("SHA256SUMS does not describe the exact publication")
        for suite_dir in sorted(path for path in (upload / "dists").iterdir() if path.is_dir()):
            verify_release(upload, keyring, suite_dir)
    if not (upload / "dists" / publication["suite"]).is_dir():
        fail("publication suite is missing")
    verify_packages(upload)
    print(f"verified {publication['channel']} publication for {publication['product']} {publication['version']}")


if __name__ == "__main__":
    main()
