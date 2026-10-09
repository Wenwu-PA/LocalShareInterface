"""One-command bootstrapper for LANBridge on Windows and Linux."""
from __future__ import annotations

import argparse
import importlib.util
import os
from pathlib import Path
import subprocess
import sys
import venv

ROOT = Path(__file__).resolve().parent
REQUIRED = ("fastapi", "uvicorn", "yaml", "argon2", "psutil", "multipart", "cryptography", "qrcode", "PIL")


def main() -> int:
    parser = argparse.ArgumentParser(description="LANBridge: install and start a local HTTPS server")
    parser.add_argument("--version", action="version", version=(ROOT / "VERSION").read_text().strip())
    parser.add_argument("--with-dev", action="store_true", help="Install development dependencies")
    actions = parser.add_mutually_exclusive_group()
    actions.add_argument("--install-only", action="store_true", help="Install dependencies and exit")
    actions.add_argument("--dev", action="store_true", help="Reload the server after backend source edits")
    actions.add_argument("--check", action="store_true", help="Check HTTPS startup using disposable data")
    actions.add_argument("--test", action="store_true", help="Run backend unit tests")
    actions.add_argument("--lint", action="store_true", help="Run Ruff (requires --with-dev installation)")
    actions.add_argument("--cert", action="store_true", help="Create the local certificate if absent")
    args = parser.parse_args()
    if sys.version_info < (3, 11):
        parser.error("Python 3.11 or newer is required")
    os.chdir(ROOT)
    sys.path.insert(0, str(ROOT))
    if os.environ.get("LANBRIDGE_USE_VENV") != "1":
        env_dir = ROOT / ".venv"
        env_python = env_dir / ("Scripts/python.exe" if os.name == "nt" else "bin/python")
        if not env_python.exists():
            print("LANBridge: creating .venv", flush=True)
            venv.EnvBuilder(with_pip=True).create(env_dir)
        environment = os.environ.copy()
        environment["LANBRIDGE_USE_VENV"] = "1"
        return subprocess.call([str(env_python), str(ROOT / "run.py"), *sys.argv[1:]], env=environment)
    missing = [module for module in REQUIRED if importlib.util.find_spec(module) is None]
    if missing or args.with_dev:
        requirements = ROOT / ("requirements-dev.txt" if args.with_dev else "requirements.txt")
        subprocess.check_call([sys.executable, "-m", "pip", "install", "-r", str(requirements)])
    if args.install_only:
        print("LANBridge dependencies installed")
        return 0
    if args.check:
        from scripts.smoke import main as smoke
        smoke()
        return 0
    if args.test:
        return subprocess.call([sys.executable, "-m", "unittest", "discover", "-s", "backend/tests", "-v"])
    if args.lint:
        return subprocess.call([sys.executable, "-m", "ruff", "check", "backend", "scripts", "run.py"])
    from backend.api.app import ensure_tls, main as serve
    if args.cert:
        cert, key = ensure_tls()
        print(f"Certificate: {cert}\nPrivate key: {key}")
        return 0
    serve(reload=args.dev)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
