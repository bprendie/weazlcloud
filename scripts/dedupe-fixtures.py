#!/usr/bin/env python3
"""Create small deterministic D0 fixtures and a content manifest."""

import hashlib
import json
import os
import random
import sys
from pathlib import Path


def write_noise(path: Path, size: int, seed: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("wb") as output:
        offset = 0
        while offset < size:
            count = min(64 * 1024, size - offset)
            block = hashlib.shake_256(f"weazl-d0:{seed}:{offset}".encode()).digest(count)
            output.write(block)
            offset += count


def write_repeated(path: Path) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    block = random.Random("weazl-d0-repeated-block").randbytes(256 * 1024)
    with path.open("wb") as output:
        for _ in range(8):
            output.write(block)


def write_disk_image(path: Path, revised: bool = False) -> None:
    if not revised:
        write_noise(path, 8 * 1024 * 1024, "disk-image-base")
        return
    original = path.with_name("disk-image-base.img")
    write_noise(original, 8 * 1024 * 1024, "disk-image-base")
    path.parent.mkdir(parents=True, exist_ok=True)
    with original.open("rb") as source, path.open("wb") as output:
        while block := source.read(64 * 1024):
            output.write(block)
    with path.open("r+b") as output:
        output.seek(4 * 1024 * 1024)
        output.write(hashlib.shake_256(b"weazl-d0:changed-region").digest(64 * 1024))
    original.unlink()


def write_preview_files(root: Path) -> None:
    svg = b'<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16"><rect width="16" height="16" fill="#a020f0"/></svg>\n'
    (root / "previews/sample.svg").parent.mkdir(parents=True, exist_ok=True)
    (root / "previews/sample.svg").write_bytes(svg)
    (root / "previews/notes.md").write_text("# D0 preview fixture\n\nSynthetic markdown preview.\n", encoding="utf-8")


def manifest(root: Path) -> dict:
    files = []
    for path in sorted(p for p in root.rglob("*") if p.is_file() and p.name != "manifest.json"):
        digest = hashlib.sha256()
        with path.open("rb") as source:
            for block in iter(lambda: source.read(1024 * 1024), b""):
                digest.update(block)
        files.append({"path": path.relative_to(root).as_posix(), "size": path.stat().st_size, "sha256": digest.hexdigest()})
    return {"format": 1, "generator": "scripts/dedupe-fixtures.py", "files": files}


def main() -> None:
    if len(sys.argv) != 2:
        raise SystemExit("usage: dedupe-fixtures.py OUTPUT_DIR")
    root = Path(sys.argv[1]).resolve()
    root.mkdir(parents=True, exist_ok=True)
    write_noise(root / "shared/identical.bin", 512 * 1024, "shared-identical")
    write_noise(root / "unique/alice.bin", 256 * 1024, "alice-unique")
    write_noise(root / "unique/bob.bin", 256 * 1024, "bob-unique")
    write_repeated(root / "repeated/repeated-chunks.bin")
    write_preview_files(root)
    (root / "empty/zero.bin").parent.mkdir(parents=True, exist_ok=True)
    (root / "empty/zero.bin").write_bytes(b"")
    write_disk_image(root / "disk/disk-image-v1.img")
    write_disk_image(root / "disk/disk-image-v2.img", revised=True)
    (root / "versions/same-path-v1.txt").parent.mkdir(parents=True, exist_ok=True)
    (root / "versions/same-path-v1.txt").write_text("trashed version\n", encoding="utf-8")
    (root / "versions/same-path-v2.txt").write_text("live replacement version\n", encoding="utf-8")
    (root / "batch/one.txt").parent.mkdir(parents=True, exist_ok=True)
    (root / "batch/one.txt").write_text("first file in shared batch snapshot\n", encoding="utf-8")
    (root / "batch/two.txt").write_text("second file in shared batch snapshot\n", encoding="utf-8")
    (root / "manifest.json").write_text(json.dumps(manifest(root), indent=2) + "\n", encoding="utf-8")
    os.chmod(root, 0o700)


if __name__ == "__main__":
    main()
