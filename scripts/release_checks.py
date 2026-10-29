"""Release safety checks that remain enabled under python -O."""

import re


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def release_notes(source, version):
    changelog = (source / "CHANGELOG.md").read_text(encoding="utf-8")
    heading = re.search(rf"(?m)^## {re.escape(version)}$", changelog)
    require(heading is not None, f"Missing CHANGELOG.md section for {version}")
    body = changelog[heading.end():].split("\n## ", 1)[0].strip()
    require(bool(body), f"Empty CHANGELOG.md section for {version}")
    return f"# LinkForge {version}\n\n{body}\n"
