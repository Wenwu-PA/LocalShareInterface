"""Check local Markdown links, screenshot references and configuration coverage."""
from __future__ import annotations

from pathlib import Path
import re
from urllib.parse import unquote

import yaml

ROOT = Path(__file__).resolve().parents[1]


def markdown_anchors(text: str) -> set[str]:
    """Heading slugs plus explicit anchors, with duplicate-heading suffixes."""
    text = re.sub(r"```.*?```", "", text, flags=re.S)
    anchors = set(re.findall(r'<a id="([^"]+)"', text))
    counts = {}
    for heading in re.findall(r"^#{1,6}\s+(.+?)\s*#*\s*$", text, re.M):
        slug = re.sub(r"[^\w\- ]", "", heading.lower()).replace(" ", "-")
        index = counts.get(slug, 0)
        counts[slug] = index + 1
        anchors.add(slug if not index else f"{slug}-{index}")
    return anchors


def main() -> None:
    problems = []
    documents = [ROOT / "README.md", ROOT / "README.python.md", ROOT / "CHANGELOG.md", *sorted((ROOT / "docs").rglob("*.md"))]
    for document in documents:
        contents = document.read_text(encoding="utf-8")
        contents = re.sub(r"```.*?```", "", contents, flags=re.S)
        links = re.findall(r"!?\[[^\]]*\]\(([^)\s]+)\)", contents)
        for link in links:
            if link.startswith(("https://", "http://", "mailto:")):
                continue
            relative, _, fragment = unquote(link).partition("#")
            target = (document.parent / relative).resolve() if relative else document
            if not target.is_file():
                problems.append(f"{document.name}: missing {link}")
            elif fragment and target.suffix == ".md":
                target_text = target.read_text(encoding="utf-8")
                anchors = markdown_anchors(target_text)
                if fragment not in anchors:
                    problems.append(f"{document.name}: missing anchor {link}")
    for config_path, documentation in (
        ("config.example.yaml", "README.python.md"),
        ("go-config.example.yaml", "docs/go-foundation.md"),
    ):
        documented = (ROOT / documentation).read_text(encoding="utf-8")
        # Check table keys, including combined rows such as server.cert / server.key.
        table_keys = set()
        for cell in re.findall(r"^\|([^|]+)\|", documented, re.M):
            table_keys.update(re.findall(r"[a-z_]+\.[a-z_]+", cell))
        config = yaml.safe_load((ROOT / config_path).read_text(encoding="utf-8"))
        for section, values in config.items():
            for key in values:
                if f"{section}.{key}" not in table_keys:
                    problems.append(f"{documentation}: undocumented parameter {section}.{key}")
    if problems:
        raise SystemExit("\n".join(problems))
    print(f"PASS: local links and all configuration keys in {len(documents)} documents")


if __name__ == "__main__":
    main()
