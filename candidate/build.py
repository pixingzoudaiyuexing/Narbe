#!/usr/bin/env python3
"""Export two already tested native Docker images as an offline candidate bundle."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile


SOURCE = Path(__file__).resolve().parent
ROOT = SOURCE.parent


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True, stderr=subprocess.PIPE).strip()


def inspect(image, source_sha, arch, title):
    info = json.loads(docker("image", "inspect", image))[0]
    labels = info["Config"]["Labels"]
    if info["Architecture"] != arch or info["Os"] != "linux" or labels.get("org.opencontainers.image.revision") != source_sha or labels.get("io.narbe.project") != "Narbe" or labels.get("org.opencontainers.image.title") != title:
        raise ValueError(f"image identity does not match source/architecture: {image}")
    return info


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--sha", required=True)
    p.add_argument("--arch", choices=("amd64", "arm64"), required=True)
    p.add_argument("--manager", required=True)
    p.add_argument("--probe", required=True)
    p.add_argument("--out", type=Path, required=True)
    a = p.parse_args()
    if not re.fullmatch(r"[0-9a-f]{40}", a.sha):
        p.error("--sha must be an exact commit SHA")
    if a.out.exists():
        p.error("output directory already exists")
    manager = inspect(a.manager, a.sha, a.arch, "Narbe")
    probe = inspect(a.probe, a.sha, a.arch, "Narbe Probe")
    manager_tag = "narbe/candidate:" + a.sha
    probe_tag = "narbe-probe/candidate:" + a.sha
    docker("tag", a.manager, manager_tag)
    docker("tag", a.probe, probe_tag)
    a.out.mkdir(parents=True, mode=0o700)
    with tempfile.TemporaryDirectory(prefix="narbe-candidate-build-") as tmp:
        stage = Path(tmp)
        docker("image", "save", "--output", str(stage / "manager-image.tar"), manager_tag)
        docker("image", "save", "--output", str(stage / "probe-image.tar"), probe_tag)
        (stage / "manager-image-inspect.json").write_text(json.dumps(manager, sort_keys=True) + "\n")
        (stage / "probe-image-inspect.json").write_text(json.dumps(probe, sort_keys=True) + "\n")
        (stage / "SOURCE_SHA").write_text(a.sha + "\n")
        (stage / "BETA5_COMPOSE_SHA256").write_text(sha256(ROOT / "release/compose.yaml") + "\n")
        for name in ("compose.yaml", "probe-compose.yaml"):
            shutil.copyfile(SOURCE / name, stage / name)
        manifest = {"kind": "narbe-offline-candidate", "sourceSha": a.sha, "architecture": a.arch, "manager": {"tag": manager_tag, "id": manager["Id"]}, "probe": {"tag": probe_tag, "id": probe["Id"]}}
        (stage / "manifest.json").write_text(json.dumps(manifest, sort_keys=True) + "\n")
        names = sorted(x.name for x in stage.iterdir())
        (stage / "SHA256SUMS").write_text("".join(f"{sha256(stage / name)}  {name}\n" for name in names))
        archive = a.out / f"narbe-candidate-linux-{a.arch}-{a.sha[:12]}.tar.gz"
        with tarfile.open(archive, "w:gz") as tar:
            for name in sorted(names + ["SHA256SUMS"]):
                item = stage / name
                tar.add(item, arcname=name, recursive=False)
        shutil.copyfile(SOURCE / "install.py", a.out / "install-candidate.py")
        (a.out / "SHA256SUMS").write_text(f"{sha256(archive)}  {archive.name}\n{sha256(a.out / 'install-candidate.py')}  install-candidate.py\n")
        subprocess.run([__import__("sys").executable, str(a.out / "install-candidate.py"), "--bundle", str(archive), "--expect-sha", a.sha, "--mode", "fresh", "--install-dir", str(Path(tmp) / "narbe-candidate"), "--check-only"],check=True)
        print(json.dumps({"bundle": archive.name, "architecture": a.arch, "sourceSha": a.sha, "managerId": manager["Id"], "probeId": probe["Id"]}))


if __name__ == "__main__":
    main()
