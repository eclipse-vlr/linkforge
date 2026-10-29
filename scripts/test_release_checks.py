"""Exercise release safety boundaries in normal and optimized Python."""

import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

import prepare_release
import upload_release
from release_checks import release_notes


COMMIT = "a" * 40


class ReleaseSafetyTests(unittest.TestCase):
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
