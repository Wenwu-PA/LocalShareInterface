"""Check local Markdown links, screenshot references and configuration coverage."""
from __future__ import annotations

from pathlib import Path
import re
from urllib.parse import unquote

import yaml

ROOT = Path(__file__).resolve().parents[1]


def main() -> None:
    problems = []
    documents = [ROOT / "README.md", ROOT / "CHANGELOG.md", *sorted((ROOT / "docs").rglob("*.md"))]
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
                anchors = re.findall(r'<a id="([^"]+)"', target_text)
                if fragment not in anchors:
                    problems.append(f"{document.name}: missing explicit anchor {link}")
    readme = (ROOT / "README.md").read_text(encoding="utf-8")
    config = yaml.safe_load((ROOT / "config.example.yaml").read_text(encoding="utf-8"))
    for section, values in config.items():
        for key in values:
            if f"`{section}.{key}`" not in readme:
                problems.append(f"Undocumented parameter: {section}.{key}")
    if problems:
        raise SystemExit("\n".join(problems))
    print(f"PASS: local links and all configuration keys in {len(documents)} documents")


if __name__ == "__main__":
    main()
