#!/usr/bin/env python3
"""Probe installer rejection tests and optional native Docker/TLS acceptance.

--image uses the actual supplied image, --arch/--revision bind its OCI labels.
A temporary CA is mounted only by a disposable test overlay: production has no
TLS bypass. No cloud API calls, target scans or Owner Volume changes occur.
"""
import argparse
import hashlib
import http.server
import io
import json
import os
from pathlib import Path
import secrets
import shutil
import ssl
import subprocess
import tarfile
import tempfile
import threading
import time

HERE = Path(__file__).resolve().parent
IMAGE_PREFIX = "ghcr.io/pixingzoudaiyuexing/narbe:v0.1.0-beta.6-probe@sha256:"


def prepare(path, image=IMAGE_PREFIX + "0" * 64):
    path.mkdir(parents=True, exist_ok=True)
    for filename in ("install-probe.sh", "probe-compose.yaml"):
        shutil.copy2(HERE / filename, path / filename)
    (path / "probe-image.env").write_text("NARBE_PROBE_IMAGE=" + image + "\n")


def run_installer(path, env, *args, text=""):
    return subprocess.run(["bash", str(path / "install-probe.sh"), *args], input=text,
                          text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                          env=env, timeout=240)


def offline():
    """Real shell execution: hostile files fail before any Docker mutation."""
    with tempfile.TemporaryDirectory(prefix="narbe-probe-installer-unit-") as temp:
        tmp = Path(temp)
        commands = tmp / "docker-commands"
        bin_dir = tmp / "bin"
        bin_dir.mkdir()
        (bin_dir / "docker").write_text("#!/usr/bin/env python3\nimport os,sys\nif sys.argv[1:2]==['compose'] and 'version' not in sys.argv:\n assert os.environ['NARBE_PROBE_IMAGE']=='ghcr.io/pixingzoudaiyuexing/narbe:v0.1.0-beta.6-probe@sha256:'+'0'*64\n assert os.environ['NARBE_PROBE_CONTROL_URL']=='https://narbe.example.com'\n assert os.environ['NARBE_PROBE_ALLOWED_PORTS']=='22,80,443,8443,8080'\n assert os.environ['NARBE_PROBE_NETWORK_MODE']=='bridge'\nwith open(os.environ['NARBE_TEST_COMMANDS'],'a') as f: f.write(repr(sys.argv[1:])+'\\n')\n")
        (bin_dir / "docker").chmod(0o755)
        env = dict(os.environ, PATH=str(bin_dir) + os.pathsep + os.environ["PATH"], NARBE_TEST_COMMANDS=str(commands), NARBE_PROBE_IMAGE="stale-development-image", NARBE_PROBE_CONTROL_URL="http://stale.invalid", NARBE_PROBE_ALLOWED_PORTS="1", NARBE_PROBE_NETWORK_MODE="host")
        token = secrets.token_urlsafe(40)
        install = tmp / "accepted"
        prepare(install)
        result = run_installer(install, env, text="https://narbe.example.com\n" + token + "\n")
        assert result.returncode == 0, result.stderr
        original = (install / "probe.env").read_bytes()
        original_token = (install / "secrets/probe-registration-token").read_bytes()
        assert original_token == token.encode()
        assert (install / "secrets").stat().st_mode & 0o777 == 0o700
        assert (install / "secrets/probe-registration-token").stat().st_mode & 0o777 == 0o644
        assert (install / "probe.env").stat().st_mode & 0o777 == 0o600
        for action in ("up", "restart", "status", "logs", "stop"):
            result = run_installer(install, env, action)
            assert result.returncode == 0, result.stderr
            assert token not in result.stdout + result.stderr
        assert (install / "probe.env").read_bytes() == original
        assert (install / "secrets/probe-registration-token").read_bytes() == original_token
        recorded = commands.read_text()
        assert token not in recorded and "down" not in recorded and "prune" not in recorded
        assert "--no-build" in recorded and "pull" in recorded
        rejected = 0
        for case in ("missing-lock", "latest", "shell-lock", "symlink-lock", "http-origin", "path-origin", "port-origin", "symlink-secret", "env-injection", "invalid-port", "symlink-env"):
            target = tmp / case
            prepare(target)
            stdin = "https://narbe.example.com\n" + token + "\n"
            if case == "missing-lock":
                (target / "probe-image.env").unlink()
            elif case in ("latest", "shell-lock"):
                (target / "probe-image.env").write_text("NARBE_PROBE_IMAGE=" + ("ghcr.io/pixingzoudaiyuexing/narbe:latest" if case == "latest" else "$(touch SHOULD_NOT_EXIST)") + "\n")
            elif case == "symlink-lock":
                (target / "probe-image.env").unlink()
                (target / "probe-image.env").symlink_to(install / "probe-image.env")
            elif case == "http-origin":
                stdin = "http://narbe.example.com\n" + token + "\n"
            elif case == "path-origin":
                stdin = "https://narbe.example.com/admin\n" + token + "\n"
            elif case == "port-origin":
                stdin = "https://narbe.example.com:99999\n" + token + "\n"
            elif case == "symlink-secret":
                (target / "secrets").mkdir()
                (target / "secrets/probe-registration-token").symlink_to(install / "secrets/probe-registration-token")
            elif case == "env-injection":
                (target / "probe.env").write_bytes(original + b"COMPOSE_FILE=other.yaml\n")
            elif case == "invalid-port":
                (target / "probe.env").write_bytes(original.replace(b"22,80,443,8443,8080", b"0,443"))
            elif case == "symlink-env":
                (target / "probe.env").symlink_to(install / "probe.env")
            before = commands.read_bytes()
            result = run_installer(target, env, text=stdin)
            assert result.returncode != 0, case
            assert commands.read_bytes() == before, case + " reached Docker"
            assert not (target / "SHOULD_NOT_EXIST").exists()
            assert token not in result.stdout + result.stderr
            rejected += 1
        print(json.dumps({"probeInstallerOffline": "PASS", "rejections": rejected, "persistence": "configuration and secret unchanged on repeated up/restart/stop/status/logs", "secretDisclosure": False}))


def runtime(args):
    real_docker = shutil.which("docker")
    assert real_docker, "Docker is required for --image acceptance"

    def docker(*argv):
        return subprocess.check_output([real_docker, *argv], text=True, stderr=subprocess.PIPE, timeout=60).strip()

    image = args.image
    info = json.loads(docker("image", "inspect", image))[0]
    labels = info["Config"]["Labels"]
    assert labels["io.narbe.project"] == "Narbe"
    assert labels["org.opencontainers.image.version"] == "v0.1.0-beta.6"
    assert not args.revision or labels["org.opencontainers.image.revision"] == args.revision
    native = {"aarch64": "arm64", "x86_64": "amd64"}.get(docker("info", "--format", "{{.Architecture}}"), docker("info", "--format", "{{.Architecture}}"))
    assert info["Architecture"] == native and (not args.arch or native == args.arch)
    run_id = secrets.token_hex(6)
    project = "narbe-probe-install-" + run_id
    registered = 0
    polls = []
    errors = []
    lock = threading.Lock()
    registration = secrets.token_urlsafe(40)
    identity_token = secrets.token_urlsafe(40)
    probe_id = "fixture-" + run_id
    with tempfile.TemporaryDirectory(prefix=project + "-") as temp:
        tmp = Path(temp)
        install = tmp / "install"
        # Before publish the same real native image may be a local CI tag. The
        # test overlay selects that image; the production lock remains strict.
        published = image.startswith(IMAGE_PREFIX) and len(image) == len(IMAGE_PREFIX) + 64
        prepare(install, image if published else IMAGE_PREFIX + info["Id"].split(":")[1])
        for argv in (
            ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-keyout", str(tmp / "ca.key"), "-out", str(tmp / "ca.crt"), "-subj", "/CN=Narbe disposable installer CA"],
            ["req", "-new", "-newkey", "rsa:2048", "-nodes", "-keyout", str(tmp / "server.key"), "-out", str(tmp / "server.csr"), "-subj", "/CN=host.docker.internal"],
        ):
            subprocess.run(["openssl", *argv], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        (tmp / "extensions").write_text("subjectAltName=DNS:host.docker.internal\nextendedKeyUsage=serverAuth\nbasicConstraints=CA:FALSE\n")
        subprocess.run(["openssl", "x509", "-req", "-days", "1", "-in", str(tmp / "server.csr"), "-CA", str(tmp / "ca.crt"), "-CAkey", str(tmp / "ca.key"), "-CAcreateserial", "-out", str(tmp / "server.crt"), "-extfile", str(tmp / "extensions")], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        bootstrap = project + "-roots"
        docker("create", "--name", bootstrap, "--label", "io.narbe.disposable=" + run_id, image)
        try:
            docker("cp", bootstrap + ":/etc/ssl/certs/ca-certificates.crt", str(tmp / "public-roots.crt"))
        finally:
            assert docker("inspect", bootstrap, "--format", '{{index .Config.Labels "io.narbe.disposable"}}') == run_id
            docker("rm", bootstrap)
        (tmp / "roots.crt").write_bytes((tmp / "public-roots.crt").read_bytes() + b"\n" + (tmp / "ca.crt").read_bytes())
        (tmp / "roots.crt").chmod(0o444)

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_POST(self):
                nonlocal registered
                try:
                    length = int(self.headers.get("Content-Length", "0"))
                    assert 0 < length <= 64 << 10
                    body = json.loads(self.rfile.read(length))
                    if self.path == "/probe/v1/enroll":
                        assert body.get("registrationToken") == registration
                        assert not self.headers.get("Authorization")
                        with lock:
                            registered += 1
                            assert registered == 1
                        payload = {"probeId": probe_id, "token": identity_token}
                    else:
                        assert self.path == "/probe/v1/poll"
                        assert self.headers.get("Authorization") == "Bearer " + identity_token
                        assert body.get("version") == "v0.1.0-beta.6"
                        assert {"egressIP", "egressCountry", "egressObservedAt", "egressSource"} <= body.keys()
                        with lock:
                            polls.append(body)
                        payload = {"task": None}
                    raw = json.dumps(payload).encode()
                    self.send_response(200)
                except Exception as error:
                    with lock:
                        errors.append(type(error).__name__)
                    raw = b'{"error":"fixture protocol assertion failed"}'
                    self.send_response(400)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

        server = http.server.ThreadingHTTPServer(("0.0.0.0", 0), Handler)
        server.daemon_threads = True
        tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        tls.minimum_version = ssl.TLSVersion.TLSv1_2
        tls.load_cert_chain(str(tmp / "server.crt"), str(tmp / "server.key"))
        server.socket = tls.wrap_socket(server.socket, server_side=True)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        endpoint = "https://host.docker.internal:" + str(server.server_address[1])
        (install / "probe.env").write_text(f"NARBE_PROBE_CONTROL_URL={endpoint}\nNARBE_PROBE_ALLOWED_PORTS=443\nNARBE_PROBE_NETWORK_MODE=bridge\nNARBE_PROBE_PROJECT_NAME={project}\n")
        (install / "secrets").mkdir(mode=0o700)
        (install / "secrets/probe-registration-token").write_text(registration)
        overlay = {"services": {"probe": {"image": image, "extra_hosts": ["host.docker.internal:host-gateway"], "volumes": [str(tmp / "roots.crt") + ":/etc/ssl/certs/ca-certificates.crt:ro"], "healthcheck": {"interval": "2s", "start_period": "1s"}, "labels": {"io.narbe.disposable": run_id}}}, "volumes": {"probe_data": {"labels": {"io.narbe.disposable": run_id}}}}
        overlay_path = tmp / "test-overlay.json"
        overlay_path.write_text(json.dumps(overlay))
        wrapper = tmp / "bin"
        wrapper.mkdir()
        # Only the test harness adds an overlay. The production installer does
        # not accept COMPOSE_FILE or an insecure-control-channel option.
        (wrapper / "docker").write_text("#!/usr/bin/env python3\nimport os,sys\na=sys.argv[1:]\nif a and a[0]=='compose' and 'version' not in a:\n for i,s in enumerate(a):\n  if s in ('pull','up','restart','stop','ps','logs'):\n   if s=='pull' and os.environ['NARBE_TEST_LOCAL']=='1': sys.exit(0)\n   a[i:i]=['-f',os.environ['NARBE_TEST_OVERLAY']]\n   break\nos.execv(os.environ['NARBE_TEST_REAL_DOCKER'],[os.environ['NARBE_TEST_REAL_DOCKER'],*a])\n")
        (wrapper / "docker").chmod(0o755)
        env = dict(os.environ, PATH=str(wrapper) + os.pathsep + os.environ["PATH"], NARBE_TEST_REAL_DOCKER=real_docker, NARBE_TEST_OVERLAY=str(overlay_path), NARBE_TEST_LOCAL="0" if published else "1")
        compose = ["compose", "--project-name", project, "--env-file", str(install / "probe.env"), "--env-file", str(install / "probe-image.env"), "-f", str(install / "probe-compose.yaml"), "-f", str(overlay_path)]
        container = None
        volume = project + "_probe_data"

        def wait_poll(previous):
            deadline = time.monotonic() + 90
            while time.monotonic() < deadline:
                with lock:
                    assert not errors, errors
                    count = len(polls)
                if count > previous and docker("inspect", container, "--format", "{{.State.Health.Status}}") == "healthy":
                    return count
                time.sleep(0.2)
            raise AssertionError("installer container heartbeat or health timed out")

        def identity():
            raw = subprocess.check_output([real_docker, "cp", container + ":/data/identity.json", "-"], stderr=subprocess.PIPE)
            with tarfile.open(fileobj=io.BytesIO(raw)) as archive:
                entry, = archive.getmembers()
                assert entry.isfile() and entry.mode == 0o600 and entry.uid == 65532
                data = archive.extractfile(entry).read()
            saved = json.loads(data)
            assert saved["identity"] == {"probeId": probe_id, "token": identity_token}
            assert saved["controlURL"] == endpoint
            return data

        try:
            result = run_installer(install, env)
            assert result.returncode == 0, result.stderr
            container = docker(*compose, "ps", "-q", "probe")
            seen = wait_poll(0)
            original = identity()
            config_before = (install / "probe.env").read_bytes()
            # After enrollment an empty one-time secret cannot re-register.
            (install / "secrets/probe-registration-token").write_text("")
            result = run_installer(install, env, "restart")
            assert result.returncode == 0, result.stderr
            seen = wait_poll(seen)
            assert identity() == original
            docker(*compose, "up", "-d", "--no-build", "--force-recreate", "--wait", "--wait-timeout", "180")
            container = docker(*compose, "ps", "-q", "probe")
            seen = wait_poll(seen)
            assert identity() == original
            result = run_installer(install, env)
            assert result.returncode == 0, result.stderr
            assert (install / "probe.env").read_bytes() == config_before
            assert (install / "secrets/probe-registration-token").read_text() == ""
            inspection = json.loads(docker("inspect", container))[0]
            host = inspection["HostConfig"]
            assert inspection["Config"]["User"] == "65532:65532"
            assert host["ReadonlyRootfs"] and host["PidsLimit"] == 64 and host["Memory"] == 96 * 1024 * 1024
            assert host["CapDrop"] == ["ALL"] and "no-new-privileges:true" in host["SecurityOpt"]
            assert not host.get("PortBindings") and not inspection["NetworkSettings"].get("Ports")
            assert host["RestartPolicy"]["Name"] == "unless-stopped"
            assert host["LogConfig"] == {"Type": "json-file", "Config": {"max-file": "2", "max-size": "5m"}}
            logs = subprocess.run([real_docker, "logs", container], check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=45)
            assert registration not in logs.stdout + logs.stderr and identity_token not in logs.stdout + logs.stderr
            assert registered == 1 and not errors
            print(json.dumps({"probeInstallerRuntime": "PASS", "image": image, "imageId": info["Id"], "architecture": native, "revision": labels["org.opencontainers.image.revision"], "publishedDigest": published, "enrollments": registered, "heartbeats": seen, "identitySHA256": hashlib.sha256(original).hexdigest(), "persistence": "same Volume and identical private identity on installer restart/repeated up and Compose force-recreate", "security": "non-root/read-only/no inbound ports/cap-drop/no-new-privileges/96m/pids64/log rotation", "https": "verified SAN and disposable CA; no production TLS bypass", "egressEvidence": polls[-1], "cloudWrites": 0, "targetChecks": 0}, ensure_ascii=False))
        finally:
            # Only exact test project resources bearing this run's label.
            for cid in docker("ps", "-aq", "--filter", "label=com.docker.compose.project=" + project).splitlines():
                assert docker("inspect", cid, "--format", '{{index .Config.Labels "io.narbe.disposable"}}') == run_id
                docker("rm", "-f", cid)
            if subprocess.run([real_docker, "volume", "inspect", volume], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
                assert docker("volume", "inspect", volume, "--format", '{{index .Labels "io.narbe.disposable"}}') == run_id
                docker("volume", "rm", volume)
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)


def main():
    parser = argparse.ArgumentParser()
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--unit", action="store_true", help="offline installer rejection and configuration-preservation tests only")
    mode.add_argument("--image", help="real built/published Probe image for native Docker/TLS acceptance")
    parser.add_argument("--arch", choices=("amd64", "arm64"))
    parser.add_argument("--revision")
    args = parser.parse_args()
    offline()
    if args.image:
        runtime(args)


if __name__ == "__main__":
    main()
