#!/usr/bin/env python3
"""Build release archives and verify the extracted Linux distribution."""

import argparse
from datetime import datetime, timezone
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import socket
import subprocess
import tarfile
import tempfile
import time
import urllib.error
import urllib.request
import zipfile


SOURCE = Path(__file__).resolve().parents[1]
PLATFORMS = [("linux", "amd64"), ("linux", "arm64"), ("windows", "amd64"),
             ("darwin", "amd64"), ("darwin", "arm64")]


def command(*args, **kwargs):
    return subprocess.check_output(args, cwd=SOURCE, text=True, **kwargs).strip()


def write_json(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")


def pack(directory, output, timestamp, windows=False):
    files = sorted(path for path in directory.rglob("*") if path.is_file())
    expected = {f"{directory.name}/{p.relative_to(directory).as_posix()}": p.read_bytes()
                for p in files}
    if windows:
        moment = datetime.fromtimestamp(timestamp, timezone.utc).timetuple()[:6]
        with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED) as archive:
            for name, data in expected.items():
                item = zipfile.ZipInfo(name, date_time=moment)
                item.create_system = 3
                item.external_attr = (0o100755 if name.endswith("/linkforge.exe")
                                      else 0o100644) << 16
                archive.writestr(item, data, compress_type=zipfile.ZIP_DEFLATED,
                                 compresslevel=9)
        with zipfile.ZipFile(output) as archive:
            assert archive.testzip() is None
            actual = {name: archive.read(name) for name in archive.namelist()}
    else:
        def normalize(info):
            info.uid = info.gid = 0
            info.uname = info.gname = ""
            info.mtime = timestamp
            info.mode = 0o755 if info.name.endswith("/linkforge") else 0o644
            return info

        with output.open("wb") as raw:
            with gzip.GzipFile(fileobj=raw, filename="", mode="wb", mtime=timestamp) as compressed:
                with tarfile.open(fileobj=compressed, mode="w") as archive:
                    for path in files:
                        archive.add(path, arcname=f"{directory.name}/{path.relative_to(directory).as_posix()}",
                                    recursive=False, filter=normalize)
        with tarfile.open(output) as archive:
            actual = {item.name: archive.extractfile(item).read()
                      for item in archive.getmembers() if item.isfile()}
    assert actual == expected, f"Archive contents differ: {output.name}"


def verify_runtime(output, version):
    # Execute the archived binary with its own migrations and working directory.
    with tempfile.TemporaryDirectory(prefix="linkforge-smoke-") as temporary:
        root = Path(temporary)
        with tarfile.open(output / f"linkforge_{version}_linux_amd64.tar.gz") as archive:
            archive.extractall(root, filter="data")
        directory = root / f"linkforge_{version}_linux_amd64"
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        env = dict(os.environ, HTTP_ADDR=f"127.0.0.1:{port}", BASE_URL=base,
                   CREATE_LINK_API_KEY="release-smoke-key")
        with (root / "server.log").open("w+") as log:
            server = subprocess.Popen([str(directory / "linkforge")], cwd=directory,
                                      env=env, stdout=log, stderr=subprocess.STDOUT)
            try:
                for _ in range(60):
                    if server.poll() is not None:
                        raise RuntimeError("Packaged server exited before becoming ready")
                    try:
                        with urllib.request.urlopen(base + "/healthz", timeout=1) as response:
                            if response.status == 200:
                                break
                    except (urllib.error.URLError, TimeoutError):
                        time.sleep(0.5)
                else:
                    raise RuntimeError("Packaged server did not become ready")
                request = urllib.request.Request(base + "/api/v1/links", method="POST",
                                                data=b'{"url":"https://example.com"}',
                                                headers={"Content-Type": "application/json"})
                try:
                    urllib.request.urlopen(request, timeout=5).close()
                except urllib.error.HTTPError as error:
                    assert error.code == 401, error.code
                else:
                    raise AssertionError("Creation without an API key was accepted")
                report_path = output / "benchmark-report.json"
                subprocess.run(["go", "run", "./cmd/loadtest", "-base", base,
                                "-requests", "500", "-workers", "10", "-out", str(report_path)],
                               cwd=SOURCE, env=env, check=True, timeout=120)
                report = json.loads(report_path.read_text())
                assert report["successful_redirects"] == report["total_requests"] == 500
                assert report["unexpected_responses"] == report["network_errors"] == 0
                with urllib.request.urlopen(base + "/api/v1/links/" + report["created_code"] + "/stats",
                                            timeout=5) as response:
                    assert json.load(response)["click_count"] == 500
                server.send_signal(signal.SIGTERM)
                assert server.wait(timeout=20) == 0
                return {"authorization_status": 401, "successful_redirects": 500,
                        "persisted_click_count": 500, "shutdown_exit_code": 0}
            finally:
                if server.poll() is None:
                    server.kill()
                    server.wait()
                log.seek(0)
                print(log.read(), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--ci-url", required=True)
    args = parser.parse_args()
    if not re.fullmatch(r"v\d+\.\d+\.\d+", args.version):
        parser.error("Version must use vMAJOR.MINOR.PATCH")
    notes = SOURCE / "docs" / f"release-{args.version}.md"
    if not notes.is_file():
        parser.error(f"Missing release notes: {notes.name}")
    output = args.output.resolve()
    if output == SOURCE or SOURCE in output.parents:
        parser.error("Output must be outside the source checkout")
    assert not command("git", "status", "--porcelain"), "Source checkout must be clean"
    output.mkdir(parents=True, exist_ok=False)
    commit = command("git", "rev-parse", "HEAD")
    timestamp = int(command("git", "show", "-s", "--format=%ct", "HEAD"))
    go_version = command("go", "env", "GOVERSION")
    expected_go = "go" + re.search(r"^go (\S+)$", (SOURCE / "go.mod").read_text(), re.M)[1]
    assert go_version == expected_go, (go_version, expected_go)
    builds = []
    with tempfile.TemporaryDirectory(prefix="linkforge-build-") as temporary:
        for system, arch in PLATFORMS:
            print(f"Building {system}/{arch}", flush=True)
            name = f"linkforge_{args.version}_{system}_{arch}"
            directory = Path(temporary) / name
            directory.mkdir()
            binary = directory / ("linkforge.exe" if system == "windows" else "linkforge")
            env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED="0")
            subprocess.run(["go", "build", "-buildvcs=true", "-trimpath", "-ldflags=-s -w",
                            "-o", str(binary), "./cmd/api"], cwd=SOURCE, env=env, check=True)
            binary.chmod(0o755)
            metadata = command("go", "version", "-m", str(binary))
            for field in (go_version, f"GOOS={system}", f"GOARCH={arch}",
                          f"vcs.revision={commit}", "vcs.modified=false"):
                assert field in metadata, field
            for source, target in [(SOURCE / "docs/running-binary.md", "README.md"),
                                   (notes, "RELEASE_NOTES.md"),
                                   (SOURCE / "docs/openapi.yaml", "openapi.yaml")]:
                shutil.copyfile(source, directory / target)
            (directory / "RELEASE_COMMIT").write_text(commit + "\n")
            shutil.copytree(SOURCE / "migrations", directory / "migrations")
            archive = output / (name + (".zip" if system == "windows" else ".tar.gz"))
            pack(directory, archive, timestamp, windows=system == "windows")
            builds.append({"os": system, "arch": arch, "archive": archive.name,
                           "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                           "metadata": metadata.replace(str(binary), binary.name, 1)})
    source_archive = output / f"linkforge-{args.version}-source.tar.gz"
    with source_archive.open("wb") as raw:
        with gzip.GzipFile(fileobj=raw, filename="", mode="wb", mtime=timestamp) as compressed:
            compressed.write(subprocess.check_output(["git", "archive", "--format=tar",
                                                      f"--prefix=linkforge-{args.version}/", commit], cwd=SOURCE))
    write_json(output / "build-manifest.json", {"version": args.version, "source_commit": commit,
                                               "go_version": go_version, "platforms": builds})
    runtime = verify_runtime(output, args.version)
    write_json(output / "verification.json", {"source_commit": commit, "go_ci": args.ci_url,
                                              "archive_contents_verified": True,
                                              "linux_amd64_smoke": runtime})
    assets = sorted(output.iterdir())
    (output / "SHA256SUMS").write_text("".join(
        hashlib.sha256(path.read_bytes()).hexdigest() + "  " + path.name + "\n" for path in assets))
    print(f"Prepared {len(assets) + 1} release assets for {commit}", flush=True)


if __name__ == "__main__":
    main()
