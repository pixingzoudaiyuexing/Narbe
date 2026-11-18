#!/usr/bin/env python3
"""Verify and install one native, offline Narbe candidate without building images."""
import argparse
from datetime import datetime, timezone
import getpass
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import shutil
import sqlite3
import stat
import subprocess
import sys
import tarfile
import tempfile
from urllib.parse import urlsplit


ENTRIES = {"manager-image.tar", "probe-image.tar", "manager-image-inspect.json", "probe-image-inspect.json", "SOURCE_SHA", "BETA5_COMPOSE_SHA256", "compose.yaml", "probe-compose.yaml", "manifest.json", "SHA256SUMS"}
BETA5_IMAGE = re.compile(r"ghcr[.]io/pixingzoudaiyuexing/narbe:v0[.]1[.]0-beta[.]5@sha256:[0-9a-f]{64}")
SHA = re.compile(r"[0-9a-f]{40}")
MAX_ENTRY = 2 * 1024**3


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def regular(path, *, max_size=None):
    st = path.lstat()
    if not stat.S_ISREG(st.st_mode) or st.st_nlink != 1:
        raise ValueError(f"not a regular, unlinked file: {path}")
    if max_size is not None and st.st_size > max_size:
        raise ValueError(f"file is too large: {path}")
    return st


def checksums(raw, expected):
    lines = raw.decode("ascii").splitlines()
    found = {}
    for line in lines:
        match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9_.-]+)", line)
        if not match or match[2] in found:
            raise ValueError("invalid or duplicate SHA256SUMS entry")
        found[match[2]] = match[1]
    if set(found) != set(expected):
        raise ValueError("SHA256SUMS file set mismatch")
    for name, path in expected.items():
        if digest(path) != found[name]:
            raise ValueError(f"SHA256 mismatch: {name}")


def verify_outer(bundle):
    regular(bundle, max_size=2 * MAX_ENTRY + 100 * 1024 * 1024)
    script = Path(__file__).resolve()
    regular(script, max_size=1024 * 1024)
    outer = bundle.parent / "SHA256SUMS"
    regular(outer, max_size=1024)
    checksums(outer.read_bytes(), {bundle.name: bundle, "install-candidate.py": script})


def verify_bundle(bundle, stage, expected_sha):
    if not SHA.fullmatch(expected_sha):
        raise ValueError("expected source SHA must contain exactly 40 lowercase hex digits")
    verify_outer(bundle)
    with tarfile.open(bundle, "r:gz") as archive:
        members = archive.getmembers()
        if len(members) != len(ENTRIES) or {m.name for m in members} != ENTRIES:
            raise ValueError("candidate archive has missing, extra or duplicate entries")
        for member in members:
            if not member.isfile() or member.name.startswith("/") or "/" in member.name or ".." in Path(member.name).parts or member.size > MAX_ENTRY or member.size < 0:
                raise ValueError("unsafe candidate archive entry")
            output = stage / member.name
            with archive.extractfile(member) as source, output.open("xb") as target:
                shutil.copyfileobj(source, target, 1024 * 1024)
    checksums((stage / "SHA256SUMS").read_bytes(), {name: stage / name for name in ENTRIES - {"SHA256SUMS"}})
    source_sha = (stage / "SOURCE_SHA").read_text("ascii").strip()
    manifest = json.loads((stage / "manifest.json").read_text("utf-8"))
    if source_sha != expected_sha or manifest.get("sourceSha") != expected_sha or manifest.get("kind") != "narbe-offline-candidate":
        raise ValueError("candidate source identity mismatch")
    arch = manifest.get("architecture")
    if arch not in ("amd64", "arm64"):
        raise ValueError("unsupported candidate architecture")
    for role in ("manager", "probe"):
        info = json.loads((stage / f"{role}-image-inspect.json").read_text("utf-8"))
        labels = info.get("Config", {}).get("Labels", {})
        wanted = ("narbe/candidate:" if role == "manager" else "narbe-probe/candidate:") + expected_sha
        title = "Narbe" if role == "manager" else "Narbe Probe"
        if manifest.get(role) != {"tag": wanted, "id": info.get("Id")} or info.get("Os") != "linux" or info.get("Architecture") != arch or labels.get("org.opencontainers.image.revision") != expected_sha or labels.get("io.narbe.project") != "Narbe" or labels.get("org.opencontainers.image.title") != title:
            raise ValueError(f"candidate {role} image identity mismatch")
        verify_docker_archive(stage / f"{role}-image.tar", wanted, info["Id"])
    if not re.fullmatch(r"[0-9a-f]{64}", (stage / "BETA5_COMPOSE_SHA256").read_text("ascii").strip()):
        raise ValueError("invalid beta.5 Compose fingerprint")
    return manifest


def verify_docker_archive(path, tag, image_id):
    with tarfile.open(path, "r:") as archive:
        members = archive.getmembers()
        if not members or len(members) > 2000:
            raise ValueError("Docker image archive has invalid member count")
        names = [m.name for m in members]
        if len(names) != len(set(names)) or "manifest.json" not in names:
            raise ValueError("Docker image archive has missing or duplicate manifest")
        for member in members:
            if member.name.startswith("/") or ".." in Path(member.name).parts or member.name.startswith("./") or not (member.isfile() or member.isdir()) or member.size < 0 or member.size > MAX_ENTRY:
                raise ValueError("unsafe Docker image archive entry")
        index = json.load(archive.extractfile("manifest.json"))
        if not isinstance(index, list) or len(index) != 1:
            raise ValueError("Docker image archive must contain exactly one image")
        record = index[0]
        config_name = record.get("Config")
        if record.get("RepoTags") != [tag] or not isinstance(config_name, str) or config_name not in names or not (re.fullmatch(r"[0-9a-f]{64}[.]json", config_name) or re.fullmatch(r"blobs/sha256/[0-9a-f]{64}", config_name)):
            raise ValueError("Docker image archive tag/config differs from inspect")
        config_bytes = archive.extractfile(config_name).read()
        config_digest = hashlib.sha256(config_bytes).hexdigest()
        if config_digest != Path(config_name).name.removesuffix(".json"):
            raise ValueError("Docker image archive config digest differs from path")
        if "index.json" in names:
            index = json.load(archive.extractfile("index.json"))
            manifests = index.get("manifests")
            if not isinstance(manifests, list) or len(manifests) != 1 or manifests[0].get("digest") != image_id:
                raise ValueError("Docker OCI index image ID differs from inspect")
            manifest_blob = "blobs/sha256/" + image_id.removeprefix("sha256:")
            if manifest_blob not in names or hashlib.sha256(archive.extractfile(manifest_blob).read()).hexdigest() != image_id.removeprefix("sha256:"):
                raise ValueError("Docker OCI manifest digest differs from inspect")
        elif config_digest != image_id.removeprefix("sha256:"):
            raise ValueError("Docker image config ID differs from inspect")
        layers = record.get("Layers")
        if not isinstance(layers, list) or not layers or any(layer not in names for layer in layers) or config_name not in names:
            raise ValueError("Docker image archive is incomplete")


def install_path(raw, mode):
    path = Path(raw)
    if not path.is_absolute() or path == Path("/") or path.name != {"fresh": "narbe-candidate", "probe": "narbe-probe-candidate"}.get(mode, path.name):
        raise ValueError("use an absolute, explicit Narbe installation directory")
    current = Path("/")
    for part in path.parts[1:]:
        current /= part
        if current.is_symlink():
            raise ValueError(f"symbolic-link path component: {current}")
        if current.exists() and not current.is_dir():
            raise ValueError(f"non-directory path component: {current}")
    if mode == "upgrade" and not path.is_dir():
        raise ValueError("beta.5 installation directory does not exist")
    if mode != "upgrade" and path.exists():
        raise ValueError("new installation directory already exists")
    if not path.parent.is_dir():
        raise ValueError("installation parent directory does not exist")
    return path


def trusted_input(path):
    path = Path(path)
    regular(path, max_size=4096)
    if path.stat().st_mode & 0o077:
        raise ValueError("secret input file must be private (mode 0600 or stricter)")
    return path.read_text("utf-8").rstrip("\n")


def secret_file(path, value):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0), 0o600)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as out:
            out.write(value)
        path.chmod(0o644)  # Compose secrets need to be readable by the non-root image.
    except BaseException:
        path.unlink(missing_ok=True)
        raise


def docker(*args, check=True):
    result = subprocess.run(["docker", *args], text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
    if check and result.returncode:
        raise RuntimeError(f"docker {' '.join(args[:2])} failed: {result.stderr.strip()}")
    return result


def compose(path, file, *args):
    env = {k: v for k, v in os.environ.items() if not k.startswith(("COMPOSE_", "NARBE_"))}
    cmd = ["docker", "compose", "--project-directory", str(path), "--env-file", str(path / ".env"), "--env-file", str(path / "image.env"), "-f", str(path / file), *args]
    result = subprocess.run(cmd, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode:
        raise RuntimeError(f"Compose {args[0]} failed: {result.stderr.strip()}")
    return result.stdout.strip()


def verify_loaded(manifest):
    native = docker("info", "--format", "{{.Architecture}}").stdout.strip()
    native = {"x86_64": "amd64", "aarch64": "arm64"}.get(native, native)
    if native != manifest["architecture"]:
        raise ValueError(f"Docker architecture {native} does not match bundle {manifest['architecture']}")
    for role in ("manager", "probe"):
        info = json.loads(docker("image", "inspect", manifest[role]["tag"]).stdout)[0]
        if info["Id"] != manifest[role]["id"] or info["Architecture"] != native or info["Config"]["Labels"].get("org.opencontainers.image.revision") != manifest["sourceSha"]:
            raise ValueError(f"loaded {role} image differs from verified bundle")


def ensure_no_project(project, volume):
    if docker("ps", "-aq", "--filter", "label=com.docker.compose.project=" + project).stdout.strip():
        raise ValueError(f"Compose project {project} already exists")
    if docker("volume", "inspect", volume, check=False).returncode == 0:
        raise ValueError(f"Docker volume {volume} already exists")
    if docker("network", "inspect", project + "_default", check=False).returncode == 0:
        raise ValueError(f"Docker network {project}_default already exists")


def origin(value):
    parsed = urlsplit(value)
    try:
        _ = parsed.port
    except ValueError as error:
        raise ValueError("invalid origin port") from error
    if parsed.scheme not in ("http", "https") or not parsed.hostname or parsed.username or parsed.password or parsed.path or parsed.query or parsed.fragment or any(c.isspace() for c in value):
        raise ValueError("origin must be an exact HTTP(S) origin")
    if parsed.scheme == "http" and parsed.hostname not in ("localhost", "127.0.0.1", "::1"):
        raise ValueError("remote origin must use HTTPS")
    return value


def write_receipt(path, manifest, mode, **extra):
    receipt = {"kind": "narbe-offline-candidate", "mode": mode, "sourceSha": manifest["sourceSha"], "architecture": manifest["architecture"], "managerImage": manifest["manager"], "probeImage": manifest["probe"], "installedAt": datetime.now(timezone.utc).isoformat(), **extra}
    (path / "candidate-install.json").write_text(json.dumps(receipt, sort_keys=True, indent=2) + "\n")


def install_fresh(stage, target, manifest, args):
    ensure_no_project("narbe-candidate", "narbe-candidate_narbe_data")
    if args.bind != "127.0.0.1":
        raise ValueError("candidate manager binds loopback only; use a trusted HTTPS reverse proxy")
    if not (1 <= args.port <= 65535):
        raise ValueError("invalid management port")
    url = origin(args.origin or f"http://localhost:{args.port}")
    password = trusted_input(args.password_file) if args.password_file else getpass.getpass("设置管理员密码（至少12字符）: ")
    if not args.password_file and password != getpass.getpass("再次输入: "):
        raise ValueError("passwords do not match")
    if not 12 <= len(password) <= 256 or "\n" in password or "\r" in password:
        raise ValueError("admin password length/content invalid")
    target.mkdir(mode=0o700)
    (target / "secrets").mkdir(mode=0o700)
    shutil.copyfile(stage / "compose.yaml", target / "compose.yaml")
    (target / ".env").write_text(f"NARBE_BIND=127.0.0.1\nNARBE_PORT={args.port}\nNARBE_ORIGIN={url}\n")
    (target / "image.env").write_text("NARBE_IMAGE=" + manifest["manager"]["tag"] + "\n")
    secret_file(target / "secrets/admin-password", password)
    compose(target, "compose.yaml", "up", "-d", "--no-build", "--pull", "never", "--wait")
    write_receipt(target, manifest, "fresh")


def install_probe(stage, target, manifest, args):
    ensure_no_project("narbe-probe-candidate", "narbe-probe-candidate_probe_data")
    if not args.control_url or urlsplit(origin(args.control_url)).scheme != "https":
        raise ValueError("Probe requires a trusted HTTPS control origin")
    if not args.registration_file:
        raise ValueError("Probe registration file is required")
    token = trusted_input(args.registration_file)
    if not 16 <= len(token) <= 4096 or any(c.isspace() for c in token):
        raise ValueError("invalid Probe registration token")
    target.mkdir(mode=0o700)
    (target / "secrets").mkdir(mode=0o700)
    shutil.copyfile(stage / "probe-compose.yaml", target / "probe-compose.yaml")
    (target / ".env").write_text("NARBE_PROBE_CONTROL_URL=" + args.control_url + "\n")
    (target / "image.env").write_text("NARBE_PROBE_IMAGE=" + manifest["probe"]["tag"] + "\n")
    secret_file(target / "secrets/probe-registration-token", token)
    compose(target, "probe-compose.yaml", "up", "-d", "--no-build", "--pull", "never", "--wait")
    write_receipt(target, manifest, "probe")


def known_beta5(target, stage):
    required = [target / name for name in ("compose.yaml", ".env", "image.env", "secrets/admin-password")]
    for path in required:
        regular(path)
    if digest(target / "compose.yaml") != (stage / "BETA5_COMPOSE_SHA256").read_text("ascii").strip():
        raise ValueError("existing Compose differs from supported beta.5 installation")
    contents = (target / "image.env").read_text("ascii").strip().splitlines()
    if len(contents) != 1 or not contents[0].startswith("NARBE_IMAGE=") or not BETA5_IMAGE.fullmatch(contents[0][len("NARBE_IMAGE="):]):
        raise ValueError("existing image lock is not beta.5")
    return contents[0][len("NARBE_IMAGE="):]


def upgrade(target, stage, manifest):
    old_image = known_beta5(target, stage)
    config = json.loads(compose(target, "compose.yaml", "config", "--format", "json"))
    if config.get("name") != "narbe" or set(config.get("services", {})) != {"narbe"} or config["services"]["narbe"].get("image") != old_image:
        raise ValueError("existing Compose project or image identity is unexpected")
    service = config["services"]["narbe"]
    mounts = service.get("volumes", [])
    if len(mounts) != 1 or mounts[0].get("target") != "/data" or mounts[0].get("type") != "volume" or mounts[0].get("source") != "narbe_data":
        raise ValueError("existing data mount is not the supported named volume")
    volume = config.get("volumes", {}).get("narbe_data", {}).get("name")
    if not volume:
        raise ValueError("existing data volume name is unknown")
    cid = compose(target, "compose.yaml", "ps", "-q", "narbe")
    if not cid:
        raise ValueError("existing beta.5 service is not running")
    old = json.loads(docker("inspect", cid).stdout)[0]
    labels = old["Config"].get("Labels", {})
    if not old["State"]["Running"] or labels.get("com.docker.compose.project") != "narbe" or labels.get("com.docker.compose.service") != "narbe" or labels.get("io.narbe.project") != "Narbe" or old["Config"]["Image"] != old_image:
        raise ValueError("running container does not match the beta.5 Compose project")
    data = [m for m in old["Mounts"] if m["Destination"] == "/data"]
    if len(data) != 1 or data[0].get("Type") != "volume" or data[0].get("Name") != volume:
        raise ValueError("running container data volume differs from Compose")
    volume_info = json.loads(docker("volume", "inspect", volume).stdout)[0]
    if volume_info.get("Labels", {}).get("io.narbe.project") != "Narbe" or volume_info.get("Labels", {}).get("com.docker.compose.project") != "narbe":
        raise ValueError("data volume ownership could not be verified")
    ports = service.get("ports", [])
    actual = old["HostConfig"].get("PortBindings", {}).get("8080/tcp", [])
    if len(ports) != 1 or len(actual) != 1 or str(ports[0].get("published")) != actual[0].get("HostPort") or ports[0].get("host_ip") != actual[0].get("HostIp"):
        raise ValueError("actual published port differs from preserved Compose configuration")
    running_env = dict(item.split("=", 1) for item in old["Config"].get("Env", []) if "=" in item)
    if running_env.get("NARBE_ORIGIN") != service.get("environment", {}).get("NARBE_ORIGIN"):
        raise ValueError("running Origin differs from preserved Compose configuration")
    backup = target / "backups" / (datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ") + "-" + os.urandom(4).hex())
    if (target / "backups").is_symlink():
        raise ValueError("backup directory must not be a symlink")
    (target / "backups").mkdir(mode=0o700, exist_ok=True)
    backup.mkdir(mode=0o700)
    compose(target, "compose.yaml", "stop", "narbe")
    for name in ("compose.yaml", ".env", "image.env", "secrets/admin-password"):
        destination = backup / name
        destination.parent.mkdir(mode=0o700, exist_ok=True)
        shutil.copyfile(target / name, destination)
        destination.chmod(0o600)
    (backup / "data").mkdir(mode=0o700)
    docker("cp", cid + ":/data/.", str(backup / "data"))
    for item in (backup / "data").rglob("*"):
        if not item.is_file() or item.is_symlink():
            raise ValueError("backup contains an unexpected data entry; service remains stopped")
    for name in ("narbe.db", "master.key"):
        if not (backup / "data" / name).is_file() or (backup / "data" / name).stat().st_size == 0:
            raise ValueError(f"backup lacks {name}; service remains stopped")
    db = sqlite3.connect(f"file:{backup / 'data/narbe.db'}?mode=rw", uri=True)
    try:
        if db.execute("PRAGMA quick_check").fetchone() != ("ok",):
            raise ValueError("backup SQLite integrity check failed; service remains stopped")
    finally:
        db.close()
    (target / "image.env").write_text("NARBE_IMAGE=" + manifest["manager"]["tag"] + "\n")
    compose(target, "compose.yaml", "up", "-d", "--no-build", "--pull", "never", "--wait")
    new_id = compose(target, "compose.yaml", "ps", "-q", "narbe")
    new = json.loads(docker("inspect", new_id).stdout)[0]
    after = [m for m in new["Mounts"] if m["Destination"] == "/data"]
    new_env = dict(item.split("=", 1) for item in new["Config"].get("Env", []) if "=" in item)
    if len(after) != 1 or after[0].get("Name") != volume or new["Image"] != manifest["manager"]["id"] or new["HostConfig"].get("PortBindings", {}).get("8080/tcp") != actual or new_env.get("NARBE_ORIGIN") != running_env.get("NARBE_ORIGIN"):
        raise ValueError("upgraded container image, data volume, port or Origin differs; inspect backup and service")
    write_receipt(target, manifest, "upgrade", previousImage=old_image, backup=str(backup), volume=volume)
    print("升级前完整备份：" + str(backup))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bundle", type=Path, required=True)
    parser.add_argument("--expect-sha", required=True)
    parser.add_argument("--mode", choices=("fresh", "upgrade", "probe"), required=True)
    parser.add_argument("--install-dir", required=True)
    parser.add_argument("--check-only", action="store_true")
    parser.add_argument("--password-file")
    parser.add_argument("--registration-file")
    parser.add_argument("--control-url")
    parser.add_argument("--bind", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8080)
    parser.add_argument("--origin")
    args = parser.parse_args()
    if any(k.startswith(("COMPOSE_", "NARBE_")) for k in os.environ):
        parser.error("unset COMPOSE_* and NARBE_* environment overrides before installation")
    target = install_path(args.install_dir, args.mode)
    bundle = args.bundle.absolute()
    with tempfile.TemporaryDirectory(prefix="narbe-candidate-verify-") as temp:
        stage = Path(temp)
        manifest = verify_bundle(bundle, stage, args.expect_sha)
        if args.check_only:
            print(json.dumps({"verified": True, "sourceSha": manifest["sourceSha"], "architecture": manifest["architecture"]}))
            return
        docker("info", "--format", "{{.Architecture}}")
        docker("compose", "version")
        if args.mode == "upgrade":
            known_beta5(target, stage)
        elif args.mode == "probe":
            if not args.control_url or urlsplit(origin(args.control_url)).scheme != "https" or not args.registration_file:
                raise ValueError("Probe needs HTTPS control origin and a private registration file")
            trusted_input(args.registration_file)
        for role in ("manager", "probe"):
            docker("image", "load", "--input", str(stage / f"{role}-image.tar"))
        verify_loaded(manifest)
        if args.mode == "fresh":
            install_fresh(stage, target, manifest, args)
        elif args.mode == "probe":
            install_probe(stage, target, manifest, args)
        else:
            upgrade(target, stage, manifest)
    print("候选安装完成。请核实容器、登录和业务行为；这不是正式发布。")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, RuntimeError, tarfile.TarError, json.JSONDecodeError) as error:
        print("候选安装停止：" + str(error), file=sys.stderr)
        sys.exit(1)
