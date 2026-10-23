#!/usr/bin/env python3
"""Upload verified distributions to a draft release without publishing it."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile


SOURCE = Path(__file__).resolve().parents[1]


def gh(*args):
    return subprocess.check_output(["gh", *args], cwd=SOURCE, text=True).strip()


def verify_release_tag(repository, version, commit):
    # matching-refs returns [] for a missing tag; access/network failures still
    # raise an error. Filter exactly because the endpoint also matches prefixes.
    references = json.loads(gh("api", f"repos/{repository}/git/matching-refs/tags/{version}"))
    reference = next((ref for ref in references if ref["ref"] == f"refs/tags/{version}"), None)
    if reference is None:
        return  # A draft may have no tag until it is published.
    target = reference["object"]
    while target["type"] == "tag":
        tag = json.loads(gh("api", f"repos/{repository}/git/tags/{target['sha']}"))
        target = tag["object"]
    if target["type"] != "commit" or target["sha"] != commit:
        raise RuntimeError(f"Release tag {version} does not resolve to {commit}; refusing to use it")


def prepare_release_notes(notes, repository, commit):
    # Exclude the repository-only status banner and publication procedure.
    notes = "\n".join(line for line in notes.splitlines()
                      if not line.startswith(("> Кандидат", "> Опубликован")))
    notes = notes.split("## Подготовка и публикация")[0].rstrip()
    # Relative repository links do not resolve from a GitHub release page.
    openapi_url = f"https://github.com/{repository}/blob/{commit}/docs/openapi.yaml"
    return notes.replace("](openapi.yaml)", f"]({openapi_url})")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--directory", required=True, type=Path)
    args = parser.parse_args()
    repository = os.environ["GITHUB_REPOSITORY"]
    commit = gh("api", f"repos/{repository}/commits/HEAD", "--jq", ".sha")
    local_commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=SOURCE, text=True).strip()
    assert commit == local_commit, "The default branch moved; prepare the new commit instead"
    verification = json.loads((args.directory / "verification.json").read_text())
    assert verification["source_commit"] == commit
    manifest = json.loads((args.directory / "build-manifest.json").read_text())
    assert manifest["source_commit"] == commit and manifest["version"] == args.version
    subprocess.run(["sha256sum", "-c", "SHA256SUMS"], cwd=args.directory, check=True)
    assets = sorted(path for path in args.directory.iterdir() if path.is_file())
    assert len(assets) == 10, "Expected five native archives, source, two manifests, benchmark and checksums"
    # List drafts through the API too: an unpublished tag may not exist yet.
    releases = json.loads(gh("api", "--paginate", "--slurp", f"repos/{repository}/releases?per_page=100"))
    matches = [release for page in releases for release in page if release["tag_name"] == args.version]
    assert len(matches) <= 1, "Multiple releases have the same tag"
    release = matches[0] if matches else None
    if release:
        assert release["draft"], "Refusing to modify a published release"
        assert release["target_commitish"] == commit or not release["assets"], (
            "A draft with assets already targets another commit")
    verify_release_tag(repository, args.version, commit)
    notes = (SOURCE / "docs" / f"release-{args.version}.md").read_text()
    notes = prepare_release_notes(notes, repository, commit)
    notes += (f"\n\n## Дистрибутивы и подтверждение сборки\n\n"
              f"Linux и macOS: amd64/arm64; Windows: amd64. Инструкция и миграции включены в архивы.\n\n"
              f"Исходники: `{commit}`. [Go CI]({verification['go_ci']}). "
              "Распакованный Linux-дистрибутив проверен с PostgreSQL и Redis: авторизация, "
              "500/500 перенаправлений, сохранение статистики и корректная остановка. "
              "Отчёт, параметры сборки и SHA256SUMS приложены.\n")
    with tempfile.NamedTemporaryFile(mode="w", suffix=".md") as body:
        body.write(notes)
        body.flush()
        if release:
            gh("release", "edit", args.version, "--repo", repository, "--draft",
               "--target", commit, "--title", f"LinkForge {args.version}", "--notes-file", body.name)
        else:
            gh("release", "create", args.version, "--repo", repository, "--draft",
               "--target", commit, "--title", f"LinkForge {args.version}", "--notes-file", body.name)
    gh("release", "upload", args.version, "--repo", repository, "--clobber", *map(str, assets))
    release = json.loads(gh("release", "view", args.version, "--repo", repository,
                            "--json", "apiUrl"))
    remote = json.loads(gh("api", release["apiUrl"]))
    assert remote["draft"] and remote["target_commitish"] == commit
    expected = {path.name: (path.stat().st_size, "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest())
                for path in assets}
    actual = {asset["name"]: (asset["size"], asset["digest"]) for asset in remote["assets"]
              if asset["state"] == "uploaded"}
    assert actual == expected, "Remote assets or their checksums differ"
    verify_release_tag(repository, args.version, commit)
    print(f"Verified {len(actual)} assets: {remote['html_url']}")
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a") as output:
            output.write(f"[Черновик LinkForge {args.version}]({remote['html_url']})\n\n"
                         f"Все {len(actual)} файлов загружены; размеры и SHA-256 проверены.\n")


if __name__ == "__main__":
    main()
