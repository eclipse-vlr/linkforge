"""Release integrity checks; no GitHub credentials or network calls are used."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import upload_release


REPOSITORY = "example/linkforge"
VERSION = "v0.1.0"
COMMIT = "a" * 40
OTHER_COMMIT = "b" * 40


def reference(kind="commit", sha=COMMIT, version=VERSION):
    return {"ref": f"refs/tags/{version}", "object": {"type": kind, "sha": sha}}


class ReleaseNotesTests(unittest.TestCase):
    def test_notes_are_ready_for_a_release_page(self):
        for status in ("> Кандидат в первый релиз.", "> Опубликован 23 октября 2026 года."):
            with self.subTest(status=status):
                source = (f"# LinkForge {VERSION}\n\n{status}\n\n"
                          "[Описание API](openapi.yaml).\n\n"
                          "[Go](https://go.dev/).\n\n"
                          "## Ограничения\n\nСтатистика публичная.\n\n"
                          "## Подготовка и публикация\n\nСлужебная инструкция.")
                notes = upload_release.prepare_release_notes(source, REPOSITORY, COMMIT)
                self.assertIn(
                    f"[Описание API](https://github.com/{REPOSITORY}/blob/{COMMIT}/docs/openapi.yaml)",
                    notes)
                self.assertIn("[Go](https://go.dev/)", notes)
                self.assertIn("Статистика публичная.", notes)
                self.assertNotIn(status, notes)
                self.assertNotIn("Служебная инструкция", notes)
                self.assertNotIn("## Подготовка и публикация", notes)


class ReleaseTagTests(unittest.TestCase):
    def test_missing_tag_and_prefix_only_are_allowed(self):
        for refs in ([], [reference(sha=OTHER_COMMIT, version=VERSION + "-rc1")]):
            with self.subTest(refs=refs), patch.object(upload_release, "gh", return_value=json.dumps(refs)):
                upload_release.verify_release_tag(REPOSITORY, VERSION, COMMIT)

    def test_lightweight_tag_must_match_commit(self):
        with patch.object(upload_release, "gh", return_value=json.dumps([reference()])):
            upload_release.verify_release_tag(REPOSITORY, VERSION, COMMIT)
        with patch.object(upload_release, "gh", return_value=json.dumps([reference(sha=OTHER_COMMIT)])):
            with self.assertRaisesRegex(RuntimeError, "Release tag"):
                upload_release.verify_release_tag(REPOSITORY, VERSION, COMMIT)

    def test_annotated_tags_are_resolved_to_commit(self):
        for final_commit in (COMMIT, OTHER_COMMIT):
            with self.subTest(commit=final_commit):
                objects = {
                    f"repos/{REPOSITORY}/git/matching-refs/tags/{VERSION}": [reference("tag", "c" * 40)],
                    f"repos/{REPOSITORY}/git/tags/{'c' * 40}": {"object": {"type": "tag", "sha": "d" * 40}},
                    f"repos/{REPOSITORY}/git/tags/{'d' * 40}": {"object": {"type": "commit", "sha": final_commit}},
                }

                def read_object(command, endpoint):
                    self.assertEqual(command, "api")
                    return json.dumps(objects[endpoint])

                with patch.object(upload_release, "gh", side_effect=read_object):
                    if final_commit == COMMIT:
                        upload_release.verify_release_tag(REPOSITORY, VERSION, COMMIT)
                    else:
                        with self.assertRaisesRegex(RuntimeError, "Release tag"):
                            upload_release.verify_release_tag(REPOSITORY, VERSION, COMMIT)

    def test_non_commit_tag_is_rejected(self):
        with patch.object(upload_release, "gh", return_value=json.dumps([reference("tree")])):
            with self.assertRaisesRegex(RuntimeError, "Release tag"):
                upload_release.verify_release_tag(REPOSITORY, VERSION, COMMIT)

    def test_api_failure_is_not_treated_as_missing_tag(self):
        error = subprocess.CalledProcessError(1, ["gh", "api"])
        with patch.object(upload_release, "gh", side_effect=error):
            with self.assertRaises(subprocess.CalledProcessError):
                upload_release.verify_release_tag(REPOSITORY, VERSION, COMMIT)

    def test_mismatched_tag_stops_before_any_release_mutation(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            (directory / "verification.json").write_text(json.dumps({"source_commit": COMMIT}))
            (directory / "build-manifest.json").write_text(json.dumps({"source_commit": COMMIT, "version": VERSION}))
            for index in range(8):
                (directory / f"asset-{index}").write_bytes(b"test asset")

            def fake_gh(*args):
                if args[:2] == ("api", f"repos/{REPOSITORY}/commits/HEAD"):
                    return COMMIT
                if args[:3] == ("api", "--paginate", "--slurp"):
                    return "[[]]"
                if args == ("api", f"repos/{REPOSITORY}/git/matching-refs/tags/{VERSION}"):
                    return json.dumps([reference(sha=OTHER_COMMIT)])
                self.fail(f"Release mutation or unexpected command before tag verification: {args}")

            argv = ["upload_release.py", "--version", VERSION, "--directory", str(directory)]
            with (patch.dict(os.environ, GITHUB_REPOSITORY=REPOSITORY),
                  patch.object(sys, "argv", argv),
                  patch.object(upload_release, "gh", side_effect=fake_gh),
                  patch.object(subprocess, "check_output", return_value=COMMIT),
                  patch.object(subprocess, "run")):
                with self.assertRaisesRegex(RuntimeError, "Release tag"):
                    upload_release.main()


if __name__ == "__main__":
    unittest.main()
