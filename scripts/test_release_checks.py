"""Exercise release safety boundaries in normal and optimized Python."""

import json
import os
from pathlib import Path
import re
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import zipfile

import prepare_release
import upload_release
from release_checks import release_notes


COMMIT = "a" * 40


class ReleaseSafetyTests(unittest.TestCase):
    def test_packaged_release_notes_resolve_their_local_links(self):
        with tempfile.TemporaryDirectory() as temporary:
            source = Path(temporary) / "source"
            (source / "docs").mkdir(parents=True)
            (source / "docs/running-binary.md").write_text("Run the server.", encoding="utf-8")
            (source / "docs/openapi.yaml").write_text("openapi: 3.0.3\n", encoding="utf-8")
            bundle = Path(temporary) / "linkforge_v0.1.2"
            bundle.mkdir()
            with patch.object(prepare_release, "SOURCE", source):
                prepare_release.write_distribution_docs(bundle, "# Release\n\n[OpenAPI](docs/openapi.yaml).\n")
            for windows in (False, True):
                with self.subTest(windows=windows):
                    path = Path(temporary) / ("bundle.zip" if windows else "bundle.tar.gz")
                    prepare_release.pack(bundle, path, 1760000000, windows=windows)
                    if windows:
                        with zipfile.ZipFile(path) as archive:
                            contents = {name: archive.read(name) for name in archive.namelist()}
                    else:
                        with tarfile.open(path) as archive:
                            contents = {item.name: archive.extractfile(item).read()
                                        for item in archive.getmembers() if item.isfile()}
                    notes = contents[bundle.name + "/RELEASE_NOTES.md"].decode("utf-8")
                    targets = re.findall(r"\]\(([^)]+)\)", notes)
                    self.assertEqual(len(targets), 1)
                    for target in targets:
                        self.assertIn(bundle.name + "/" + target, contents)

    def test_dirty_checkout_stops_before_build(self):
        with tempfile.TemporaryDirectory() as temporary:
            source = Path(temporary) / "source"
            source.mkdir()
            (source / "CHANGELOG.md").write_text("# Changes\n\n## v0.1.2\n\nRelease notes\n", encoding="utf-8")
            argv = ["prepare_release.py", "--version", "v0.1.2", "--output",
                    str(Path(temporary) / "output"), "--ci-url", "https://example.invalid/ci"]
            with (patch.object(prepare_release, "SOURCE", source),
                  patch.object(sys, "argv", argv),
                  patch.object(prepare_release, "command", return_value=" M README.md"),
                  patch.object(prepare_release.subprocess, "run") as build):
                with self.assertRaisesRegex(RuntimeError, "Source checkout must be clean"):
                    prepare_release.main()
                build.assert_not_called()
                self.assertFalse((Path(temporary) / "output").exists())

    def test_changelog_exports_only_the_requested_version(self):
        with tempfile.TemporaryDirectory() as temporary:
            source = Path(temporary)
            (source / "CHANGELOG.md").write_text(
                "# Changes\n\n## v0.1.2\n\nCurrent fix.\n\n### Detail\n\nMore detail.\n\n## v0.1.1\n\nOld fix.\n",
                encoding="utf-8")
            notes = release_notes(source, "v0.1.2")
            self.assertIn("Current fix.", notes)
            self.assertIn("More detail.", notes)
            self.assertNotIn("Old fix.", notes)
            with self.assertRaisesRegex(RuntimeError, "Missing CHANGELOG"):
                release_notes(source, "v0.9.9")
            (source / "CHANGELOG.md").write_text("## v0.1.2\n\n## v0.1.1\nOld fix.", encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "Empty CHANGELOG"):
                release_notes(source, "v0.1.2")

    def test_upload_guards_stop_before_release_mutation(self):
        for failure in ("verification", "manifest", "asset_count", "published", "draft_commit"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary)
                verification_commit = "b" * 40 if failure == "verification" else COMMIT
                (directory / "verification.json").write_text(
                    json.dumps({"source_commit": verification_commit}), encoding="utf-8")
                manifest_version = "v0.0.0" if failure == "manifest" else "v0.1.2"
                (directory / "build-manifest.json").write_text(
                    json.dumps({"source_commit": COMMIT, "version": manifest_version}), encoding="utf-8")
                for index in range(7 if failure == "asset_count" else 8):
                    (directory / f"asset-{index}").write_bytes(b"fixture")
                release = {"tag_name": "v0.1.2", "draft": failure != "published",
                           "target_commitish": "b" * 40 if failure == "draft_commit" else COMMIT,
                           "assets": [{"name": "existing"}]}

                def fake_gh(*args):
                    if args[:2] == ("api", "repos/example/linkforge/commits/HEAD"):
                        return COMMIT
                    if args[:3] == ("api", "--paginate", "--slurp"):
                        return json.dumps([[release]])
                    self.fail(f"Release mutation or unexpected command before rejecting {failure}: {args}")

                argv = ["upload_release.py", "--version", "v0.1.2", "--directory", str(directory)]
                with (patch.dict(os.environ, GITHUB_REPOSITORY="example/linkforge"),
                      patch.object(sys, "argv", argv),
                      patch.object(upload_release, "gh", side_effect=fake_gh),
                      patch.object(upload_release.subprocess, "check_output", return_value=COMMIT),
                      patch.object(upload_release.subprocess, "run")):
                    with self.assertRaises(RuntimeError):
                        upload_release.main()


if __name__ == "__main__":
    unittest.main()
