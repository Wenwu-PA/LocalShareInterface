"""Reject private runtime files and recognizable secret material in Git's index."""
from __future__ import annotations

from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def main() -> None:
    names = subprocess.check_output(["git", "ls-files", "-z"], cwd=ROOT).decode("utf-8").split("\0")
    failures = []
    forbidden = {"data", ".venv", "node_modules", "uploads", "certs", "logs"}
    secret = re.compile(rb"-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----|\bghp_[A-Za-z0-9]{36}\b")
    for name in filter(None, names):
        path = Path(name)
        if (forbidden.intersection(path.parts) or (path.name.startswith(".env") and path.name != ".env.example")
                or path.name == "config.yaml" or path.suffix in {".db", ".sqlite", ".sqlite3", ".pem", ".crt", ".key", ".log"}):
            failures.append(name)
        contents = subprocess.check_output(["git", "show", ":" + name], cwd=ROOT)
        if secret.search(contents):
            failures.append(name + ": recognizable secret")
    examples = [".env", ".env.production", "config.yaml", "data/example.txt", "test.sqlite3", "certs/key.pem", "server.key", "debug.log"]
    for name in examples:
        if subprocess.run(["git", "check-ignore", "--no-index", "-q", name], cwd=ROOT).returncode != 0:
            failures.append(name + ": not ignored")
    if failures:
        raise SystemExit("Repository check failed:\n" + "\n".join(failures))
    print(f"PASS: {len(list(filter(None, names)))} indexed files; private paths ignored; no recognized secrets")


if __name__ == "__main__":
    main()
