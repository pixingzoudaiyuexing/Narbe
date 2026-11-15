#!/usr/bin/env python3
"""Real isolated Docker Probe acceptance. No cloud API or target scan occurs.

The HTTPS control endpoint is an explicit local fixture. Probe egress lookups
remain real: offline/unknown/overseas evidence is reported honestly, never CN.
"""
import http.server
import io
import json
import os
from pathlib import Path
import platform
import secrets
import ssl
import subprocess
import tarfile
import tempfile
import threading
import time


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True, stderr=subprocess.PIPE, timeout=45).strip()


def main():
    image = os.environ.get("NARBE_PROBE_IMAGE", "narbe-probe:cn-candidate")
    image_info = json.loads(docker("image", "inspect", image))[0]
    assert image_info["Config"]["Labels"]["io.narbe.project"] == "Narbe"
    assert image_info["Config"]["User"] == "65532:65532"
    assert not image_info["Config"].get("ExposedPorts"), "probe must have no inbound ports"
    native = docker("info", "--format", "{{.Architecture}}")
    docker_os = docker("info", "--format", "{{.OperatingSystem}}")
    architecture = {"aarch64": "arm64", "x86_64": "amd64"}.get(native, native)
    assert architecture == image_info["Architecture"], "native runtime test requires matching daemon/image architecture"
    run_id = secrets.token_hex(8)
    name = "narbe-probe-runtime-" + run_id
    volume = name + "-data"
    labels = ["--label", "io.narbe.project=Narbe", "--label", "io.narbe.disposable=true", "--label", "io.narbe.probe-runtime=" + run_id]
    lock = threading.Lock()
    registered = 0
    heartbeats = []
    protocol_errors = []
    registration = secrets.token_hex(32)
    identity_token = secrets.token_hex(32)
    probe_id = "runtime-" + run_id
    made_volume = False

    with tempfile.TemporaryDirectory(prefix="narbe-probe-runtime-") as temp:
        tmp = Path(temp)
        # A temporary CA is trusted only in this disposable container. The
        # production agent has no insecure-TLS switch and still verifies SAN.
        subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-keyout", str(tmp / "ca.key"), "-out", str(tmp / "ca.crt"), "-subj", "/CN=Narbe disposable probe runtime CA"], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        subprocess.run(["openssl", "req", "-new", "-newkey", "rsa:2048", "-nodes", "-keyout", str(tmp / "server.key"), "-out", str(tmp / "server.csr"), "-subj", "/CN=host.docker.internal"], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        (tmp / "extensions").write_text("subjectAltName=DNS:host.docker.internal\nextendedKeyUsage=serverAuth\nbasicConstraints=CA:FALSE\n")
        subprocess.run(["openssl", "x509", "-req", "-days", "1", "-in", str(tmp / "server.csr"), "-CA", str(tmp / "ca.crt"), "-CAkey", str(tmp / "ca.key"), "-CAcreateserial", "-out", str(tmp / "server.crt"), "-extfile", str(tmp / "extensions")], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        # Retain the standard public roots so actual country.is evidence works.
        bootstrap = name + "-certs"
        docker("create", "--name", bootstrap, *labels, image)
        try:
            docker("cp", bootstrap + ":/etc/ssl/certs/ca-certificates.crt", str(tmp / "public-roots.crt"))
        finally:
            assert docker("inspect", bootstrap, "--format", '{{index .Config.Labels "io.narbe.probe-runtime"}}') == run_id
            docker("rm", bootstrap)
        (tmp / "roots.crt").write_bytes((tmp / "public-roots.crt").read_bytes() + b"\n" + (tmp / "ca.crt").read_bytes())
        (tmp / "roots.crt").chmod(0o444)
        # The registration file is never copied into the image, logs or evidence.
        (tmp / "registration").write_text(registration)
        (tmp / "registration").chmod(0o444)

        class Handler(http.server.BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, *_):
                pass

            def reply(self, code, body):
                raw = json.dumps(body, separators=(",", ":")).encode()
                self.send_response(code)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.send_header("Connection", "close")
                self.end_headers()
                self.wfile.write(raw)

            def do_POST(self):
                nonlocal registered
                try:
                    length = int(self.headers.get("Content-Length", "0"))
                    assert 0 < length <= 64 << 10
                    body = json.loads(self.rfile.read(length))
                    assert self.headers.get("Content-Type", "").startswith("application/json")
                    if self.path == "/probe/v1/enroll":
                        assert body.get("registrationToken") == registration
                        assert isinstance(body.get("version"), str) and body["version"]
                        assert self.headers.get("Authorization") is None
                        with lock:
                            registered += 1
                            assert registered == 1, "identity was registered again after restart/recreate"
                        self.reply(200, {"probeId": probe_id, "token": identity_token})
                        return
                    assert self.headers.get("Authorization") == "Bearer " + identity_token
                    assert self.path == "/probe/v1/poll", "fixture issues no target task; results are unexpected"
                    assert isinstance(body.get("version"), str) and body["version"]
                    assert {"egressIP", "egressCountry", "egressObservedAt", "egressSource"} <= body.keys()
                    with lock:
                        heartbeats.append(body)
                    self.reply(200, {"task": None})
                except Exception as error:
                    with lock:
                        protocol_errors.append(type(error).__name__)
                    self.reply(400, {"error": "fixture protocol assertion failed"})

        server = http.server.ThreadingHTTPServer(("0.0.0.0", 0), Handler)
        server.daemon_threads = True
        tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        tls.minimum_version = ssl.TLSVersion.TLSv1_2
        tls.load_cert_chain(str(tmp / "server.crt"), str(tmp / "server.key"))
        server.socket = tls.wrap_socket(server.socket, server_side=True)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        endpoint = "https://host.docker.internal:" + str(server.server_address[1])

        def launch():
            docker("run", "-d", "--name", name, *labels, "--add-host", "host.docker.internal:host-gateway", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--memory", "96m", "--pids-limit", "64", "--log-driver", "json-file", "--log-opt", "max-size=5m", "--log-opt", "max-file=2", "--restart", "unless-stopped", "--health-interval", "2s", "--health-start-period", "1s", "-v", volume + ":/data", "--mount", f"type=bind,src={tmp / 'roots.crt'},dst=/etc/ssl/certs/ca-certificates.crt,readonly", "--mount", f"type=bind,src={tmp / 'registration'},dst=/run/registration,readonly", "-e", "NARBE_PROBE_CONTROL_URL=" + endpoint, "-e", "NARBE_PROBE_REGISTRATION_TOKEN_FILE=/run/registration", "-e", "NARBE_PROBE_ALLOWED_PORTS=443", image)

        def wait_heartbeat(previous):
            deadline = time.monotonic() + 75
            while time.monotonic() < deadline:
                with lock:
                    if protocol_errors:
                        raise AssertionError("fixture protocol errors: " + str(protocol_errors))
                    seen = len(heartbeats)
                if seen > previous:
                    proc = subprocess.run(["docker", "exec", name, "/narbe-probe", "healthcheck"], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                    if proc.returncode == 0 and docker("inspect", name, "--format", "{{.State.Health.Status}}") == "healthy":
                        return seen
                state = docker("inspect", name, "--format", "{{.State.Status}}")
                assert state == "running", "probe exited before healthy heartbeat"
                time.sleep(0.2)
            raise AssertionError("probe heartbeat timeout; TLS or host reachability not validated")

        def identity_bytes():
            # docker cp TAR metadata preserves the actual container UID/mode.
            raw = subprocess.check_output(["docker", "cp", name + ":/data/identity.json", "-"], stderr=subprocess.PIPE)
            with tarfile.open(fileobj=io.BytesIO(raw)) as tar:
                entries = tar.getmembers()
                assert len(entries) == 1
                info = entries[0]
                assert info.isfile() and info.mode == 0o600 and info.uid == 65532
                data = tar.extractfile(info).read()
                saved = json.loads(data)
                assert saved["identity"] == {"probeId": probe_id, "token": identity_token}
                assert saved["controlURL"] == endpoint
                return data

        try:
            docker("volume", "create", *labels, volume)
            made_volume = True
            launch()
            seen = wait_heartbeat(0)
            original_identity = identity_bytes()
            # Registration must no longer be needed; recreate reads an empty file.
            (tmp / "registration").chmod(0o600)
            (tmp / "registration").write_text("")
            (tmp / "registration").chmod(0o444)
            docker("restart", name)
            seen = wait_heartbeat(seen)
            assert identity_bytes() == original_identity
            docker("rm", "-f", name)
            launch()
            seen = wait_heartbeat(seen)
            assert identity_bytes() == original_identity
            config = json.loads(docker("inspect", name))[0]
            assert config["Config"]["User"] == "65532:65532"
            host = config["HostConfig"]
            assert host["ReadonlyRootfs"] and host["PidsLimit"] == 64
            assert host["Memory"] == 96 * 1024 * 1024
            assert host["CapDrop"] == ["ALL"]
            assert "no-new-privileges:true" in host["SecurityOpt"]
            assert not host.get("PortBindings") and not config["NetworkSettings"].get("Ports")
            assert host["RestartPolicy"]["Name"] == "unless-stopped"
            assert host["LogConfig"] == {"Type": "json-file", "Config": {"max-file": "2", "max-size": "5m"}}
            captured = subprocess.run(["docker", "logs", name], check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=45)
            logs = captured.stdout + captured.stderr
            assert registration not in logs and identity_token not in logs
            with lock:
                assert registered == 1 and not protocol_errors
                assert all(h["version"] == image_info["Config"]["Labels"]["org.opencontainers.image.version"] for h in heartbeats), "runtime version differs from image version label"
                actual_egress = [{k: h.get(k) for k in ("egressIP", "egressCountry", "egressObservedAt", "egressSource", "egressError")} for h in heartbeats]
            print(json.dumps({"status": "PASS", "image": image, "imageId": image_info["Id"], "architecture": architecture, "hostSystem": platform.system(), "dockerOperatingSystem": docker_os, "enrollments": registered, "heartbeats": seen, "httpsControl": "isolated fixture, verified temporary CA and SAN", "targetChecks": 0, "cloudWrites": 0, "security": "nonroot/read-only/no inbound ports/cap-drop/no-new-privileges/limits/log rotation", "persistence": "same Volume and byte-identical private identity after restart and recreate", "egressEvidence": actual_egress, "egressMeaning": "Actual network observation; fixture did not manufacture country/IP and does not prove mainland target connectivity"}, ensure_ascii=False))
        finally:
            if subprocess.run(["docker", "inspect", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
                assert docker("inspect", name, "--format", '{{index .Config.Labels "io.narbe.probe-runtime"}}') == run_id
                docker("rm", "-f", name)
            if made_volume:
                assert docker("volume", "inspect", volume, "--format", '{{index .Labels "io.narbe.probe-runtime"}}') == run_id
                docker("volume", "rm", volume)
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)


if __name__ == "__main__":
    main()
