#!/usr/bin/env python3
"""Offline candidate archive and installer rejection tests; no Docker mutation."""
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("candidate_install", ROOT / "candidate/install.py")
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)
SOURCE_SHA = "a" * 40


def hash_file(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def bundle(base, mutate=None):
    folder = base / "content"
    folder.mkdir()
    layer_digest="sha256:"+hashlib.sha256(b"fixture layer").hexdigest()
    manager_config = json.dumps({"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[layer_digest]},"config":{"Labels":{"io.narbe.project":"Narbe","org.opencontainers.image.title":"Narbe","org.opencontainers.image.revision":SOURCE_SHA}}}).encode()
    probe_config = json.dumps({"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[layer_digest]},"config":{"Labels":{"io.narbe.project":"Narbe","org.opencontainers.image.title":"Narbe Probe","org.opencontainers.image.revision":SOURCE_SHA}}}).encode()
    manager = {"Id": "sha256:" + hashlib.sha256(manager_config).hexdigest(), "Os": "linux", "Architecture": "amd64", "Config": {"Labels": {"io.narbe.project": "Narbe", "org.opencontainers.image.title": "Narbe", "org.opencontainers.image.revision": SOURCE_SHA}}}
    probe = {"Id": "sha256:" + hashlib.sha256(probe_config).hexdigest(), "Os": "linux", "Architecture": "amd64", "Config": {"Labels": {"io.narbe.project": "Narbe", "org.opencontainers.image.title": "Narbe Probe", "org.opencontainers.image.revision": SOURCE_SHA}}}
    manifest = {"kind": "narbe-offline-candidate", "sourceSha": SOURCE_SHA, "architecture": "amd64", "manager": {"tag": "narbe/candidate:" + SOURCE_SHA, "id": manager["Id"]}, "probe": {"tag": "narbe-probe/candidate:" + SOURCE_SHA, "id": probe["Id"]}}
    def image_tar(role, image, config):
        output = io.BytesIO()
        with tarfile.open(fileobj=output, mode="w") as tar:
            index = json.dumps([{"Config": image["Id"][7:] + ".json", "RepoTags": [manifest[role]["tag"]], "Layers": ["layer.tar"]}]).encode()
            for name, raw in (("manifest.json", index), (image["Id"][7:] + ".json", config), ("layer.tar", b"fixture layer")):
                info = tarfile.TarInfo(name)
                info.size = len(raw)
                tar.addfile(info, io.BytesIO(raw))
        return output.getvalue()
    entries = {"manager-image.tar": image_tar("manager", manager, manager_config), "probe-image.tar": image_tar("probe", probe, probe_config), "manager-image-inspect.json": json.dumps(manager).encode(), "probe-image-inspect.json": json.dumps(probe).encode(), "SOURCE_SHA": (SOURCE_SHA + "\n").encode(), "BETA5_COMPOSE_SHA256": ("d" * 64 + "\n").encode(), "compose.yaml": b"name: narbe-candidate\n", "probe-compose.yaml": b"name: narbe-probe-candidate\n", "manifest.json": json.dumps(manifest).encode()}
    if mutate:
        mutate(entries)
    entries["SHA256SUMS"] = "".join(f"{hashlib.sha256(data).hexdigest()}  {name}\n" for name, data in sorted(entries.items())).encode()
    archive = base / "candidate.tar.gz"
    with tarfile.open(archive, "w:gz") as tar:
        for name, data in entries.items():
            info = tarfile.TarInfo(name)
            info.size = len(data)
            tar.addfile(info, io.BytesIO(data))
    script = ROOT / "candidate/install.py"
    (base / "SHA256SUMS").write_text(f"{hash_file(archive)}  {archive.name}\n{hash_file(script)}  install-candidate.py\n")
    return archive


class CandidateBundle(unittest.TestCase):
    def verify(self, archive, sha=SOURCE_SHA):
        with tempfile.TemporaryDirectory() as stage:
            return installer.verify_bundle(archive, Path(stage), sha)

    def test_valid_bundle_has_exact_source_and_image_ids(self):
        with tempfile.TemporaryDirectory() as tmp:
            archive = bundle(Path(tmp))
            result = self.verify(archive)
            self.assertEqual(result["sourceSha"], SOURCE_SHA)
            self.assertEqual(result["architecture"], "amd64")

    def test_outer_hash_and_expected_source_cannot_be_changed(self):
        with tempfile.TemporaryDirectory() as tmp:
            archive = bundle(Path(tmp))
            with self.assertRaisesRegex(ValueError, "expected source SHA"):
                self.verify(archive, "main")
            with self.assertRaisesRegex(ValueError, "source identity mismatch"):
                self.verify(archive, "f" * 40)
            with archive.open("ab") as stream:
                stream.write(b"tamper")
            with self.assertRaisesRegex(ValueError, "SHA256 mismatch"):
                self.verify(archive)

    def test_malicious_entries_and_internal_hash_are_rejected(self):
        for kind in ("traversal", "symlink", "duplicate", "internal_hash"):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as tmp:
                base = Path(tmp)
                archive = bundle(base)
                if kind != "internal_hash":
                    with tarfile.open(archive, "r:gz") as tar:
                        members = [(m, tar.extractfile(m).read()) for m in tar.getmembers()]
                    with tarfile.open(archive, "w:gz") as tar:
                        for member, raw in members:
                            if kind == "traversal" and member.name == "SOURCE_SHA":
                                member.name = "../SOURCE_SHA"
                            if kind == "symlink" and member.name == "SOURCE_SHA":
                                member.type = tarfile.SYMTYPE
                                member.linkname = "/etc/passwd"
                            tar.addfile(member, io.BytesIO(raw) if member.isfile() else None)
                        if kind == "duplicate":
                            tar.addfile(members[0][0], io.BytesIO(members[0][1]))
                    (base / "SHA256SUMS").write_text(f"{hash_file(archive)}  {archive.name}\n{hash_file(ROOT / 'candidate/install.py')}  install-candidate.py\n")
                else:
                    with tarfile.open(archive, "r:gz") as tar:
                        members = [(m, tar.extractfile(m).read()) for m in tar.getmembers()]
                    with tarfile.open(archive, "w:gz") as tar:
                        for member, raw in members:
                            if member.name == "manager-image.tar":
                                raw = b"altered manager"
                                member.size = len(raw)
                            tar.addfile(member, io.BytesIO(raw))
                    (base / "SHA256SUMS").write_text(f"{hash_file(archive)}  {archive.name}\n{hash_file(ROOT / 'candidate/install.py')}  install-candidate.py\n")
                with self.assertRaises(ValueError):
                    self.verify(archive)

    def test_docker_save_rejects_extra_tag_and_traversal(self):
        with tempfile.TemporaryDirectory() as tmp:
            source = Path(tmp) / "image.tar"
            def write(tags, config_name):
                with tarfile.open(source, "w") as tar:
                    index = json.dumps([{"Config": config_name, "RepoTags": tags, "Layers": ["layer.tar"]}]).encode()
                    for name, raw in (("manifest.json", index), (config_name, b"config"), ("layer.tar", b"layer")):
                        item = tarfile.TarInfo(name)
                        item.size = len(raw)
                        tar.addfile(item, io.BytesIO(raw))
            image_id = "sha256:" + hashlib.sha256(b"config").hexdigest()
            tag = "narbe/candidate:" + SOURCE_SHA
            write([tag, "unwanted:latest"], image_id[7:] + ".json")
            with self.assertRaisesRegex(ValueError, "tag/config"):
                installer.verify_docker_archive(source, tag, image_id)
            write([tag], "../unexpected.json")
            with self.assertRaisesRegex(ValueError, "unsafe"):
                installer.verify_docker_archive(source, tag, image_id)

    def test_oci_index_accepts_config_and_manifest_ids_and_rejects_crosslinks(self):
        for id_kind in ("config", "manifest"):
            for tamper in (None, "config", "layer"):
                with self.subTest(id_kind=id_kind,tamper=tamper), tempfile.TemporaryDirectory() as tmp:
                    layer=b"real Docker save uncompressed layer fixture"
                    layer_id="sha256:"+hashlib.sha256(layer).hexdigest()
                    config=json.dumps({"rootfs":{"type":"layers","diff_ids":[layer_id]}}).encode()
                    config_id="sha256:"+hashlib.sha256(config).hexdigest()
                    oci={"schemaVersion":2,"config":{"digest":config_id,"size":len(config)},"layers":[{"digest":layer_id,"size":len(layer)}]}
                    if tamper=="config":oci["config"]["digest"]="sha256:"+"f"*64
                    if tamper=="layer":oci["layers"][0]["digest"]="sha256:"+"e"*64
                    oci_raw=json.dumps(oci).encode();manifest_id="sha256:"+hashlib.sha256(oci_raw).hexdigest()
                    config_name="blobs/sha256/"+config_id[7:];layer_name="blobs/sha256/"+layer_id[7:]
                    tag="narbe/candidate:"+SOURCE_SHA
                    entries={"manifest.json":json.dumps([{"Config":config_name,"RepoTags":[tag],"Layers":[layer_name]}]).encode(),"index.json":json.dumps({"schemaVersion":2,"manifests":[{"digest":manifest_id}]}).encode(),config_name:config,layer_name:layer,"blobs/sha256/"+manifest_id[7:]:oci_raw}
                    archive=Path(tmp)/"image.tar"
                    with tarfile.open(archive,"w") as tar:
                        for name,raw in entries.items():
                            member=tarfile.TarInfo(name);member.size=len(raw);tar.addfile(member,io.BytesIO(raw))
                    image_id=config_id if id_kind=="config" else manifest_id
                    if tamper:
                        with self.assertRaisesRegex(ValueError,"OCI"):
                            installer.verify_docker_archive(archive,tag,image_id)
                    else:
                        verified=installer.verify_docker_archive(archive,tag,image_id)
                        self.assertEqual(set(verified["allowedIds"]),{config_id,manifest_id})

    def test_config_binding_and_docker_optional_defaults(self):
        self.assertEqual(installer.canonical_config({"User":"65532:65532","AttachStdin":False,"Hostname":"","Cmd":None}),{"User":"65532:65532"})
        self.assertEqual(installer.canonical_config({"StopTimeout":0}),{"StopTimeout":0})
        with tempfile.TemporaryDirectory() as tmp:
            def mutate(entries):
                info=json.loads(entries["manager-image-inspect.json"])
                info["Config"]["Entrypoint"]=["/altered-entrypoint"]
                entries["manager-image-inspect.json"]=json.dumps(info).encode()
            archive=bundle(Path(tmp),mutate)
            with self.assertRaises(ValueError):self.verify(archive)

    def test_install_path_requires_exact_new_name_and_no_symlinks(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp).resolve()
            with self.assertRaises(ValueError):
                installer.install_path(str(base / "random"), "fresh")
            with self.assertRaises(ValueError):
                installer.install_path("relative/narbe-candidate", "fresh")
            real = base / "real"
            real.mkdir()
            (base / "linked").symlink_to(real)
            with self.assertRaisesRegex(ValueError, "symbolic-link"):
                installer.install_path(str(base / "linked/narbe-candidate"), "fresh")
            self.assertEqual(installer.install_path(str(base / "narbe-candidate"), "fresh"), base / "narbe-candidate")

    def test_upgrade_requires_exact_beta5_compose_and_image_lock(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            stage = base / "stage"
            stage.mkdir()
            (stage / "BETA5_COMPOSE_SHA256").write_text("e" * 64 + "\n")
            install = base / "existing"
            (install / "secrets").mkdir(parents=True)
            (install / "compose.yaml").write_text("changed")
            (install / ".env").write_text("NARBE_BIND=127.0.0.1\n")
            (install / "image.env").write_text("NARBE_IMAGE=latest\n")
            (install / "secrets/admin-password").write_text("private")
            with self.assertRaisesRegex(ValueError, "Compose differs"):
                installer.known_beta5(install, stage)
            (stage / "BETA5_COMPOSE_SHA256").write_text(hash_file(install / "compose.yaml") + "\n")
            with self.assertRaisesRegex(ValueError, "image lock"):
                installer.known_beta5(install, stage)
            (install / "secrets/admin-password").unlink()
            (install / "secrets/admin-password").symlink_to(install / ".env")
            with self.assertRaisesRegex(ValueError, "regular"):
                installer.known_beta5(install, stage)


if __name__ == "__main__":
    unittest.main()
