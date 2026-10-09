"""Start a real HTTPS server against disposable data and check its API."""
from __future__ import annotations

import json
import os
from contextlib import contextmanager
from pathlib import Path
import socket
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

import yaml
import psutil

ROOT = Path(__file__).resolve().parents[1]


def stop_process_tree(process: subprocess.Popen) -> None:
    """Wait for the venv redirector and its child to release Windows file handles."""
    try:
        parent = psutil.Process(process.pid)
        children = parent.children(recursive=True)
        processes = list(reversed(children)) + [parent]
        for item in processes:
            try:
                item.terminate()
            except psutil.NoSuchProcess:
                pass
        _, alive = psutil.wait_procs(processes, timeout=5)
        for item in alive:
            item.kill()
        psutil.wait_procs(alive, timeout=5)
    except psutil.NoSuchProcess:
        pass
    process.wait(timeout=10)


@contextmanager
def test_server(*, development: bool = False):
    with tempfile.TemporaryDirectory(prefix="lanbridge-smoke-") as directory:
        temporary = Path(directory)
        config = yaml.safe_load((ROOT / "config.example.yaml").read_text(encoding="utf-8"))
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        config["server"].update(host="127.0.0.1", port=port, https=True, tls_certfile="", tls_keyfile="")
        config["tunnel"].update(provider="cloudflare", cloudflared_binary=str(temporary / "missing-cloudflared"),
                               origin_url=f"https://127.0.0.1:{port}")
        for key, value in {"root": "files", "database": "test.sqlite3", "tls_dir": "tls", "logs": "logs"}.items():
            config["storage"][key] = str(temporary / value)
        config_path = temporary / "config.yaml"
        config_path.write_text(yaml.safe_dump(config), encoding="utf-8")
        environment = {key: value for key, value in os.environ.items() if not key.startswith("LANBRIDGE_")}
        environment.update(LANBRIDGE_USE_VENV="1", LANBRIDGE_CONFIG=str(config_path),
                           LANBRIDGE_ENV_FILE=str(temporary / ".env"),
                           LANBRIDGE_HOST="127.0.0.1", LANBRIDGE_PORT=str(port), PYTHONUTF8="1")
        context = ssl._create_unverified_context()  # Generated local test certificate.
        base = f"https://127.0.0.1:{port}"
        with (temporary / "server.log").open("w+", encoding="utf-8") as log:
            command = [sys.executable, str(ROOT / "run.py")]
            if development:
                command.append("--dev")
            process = subprocess.Popen(command, cwd=ROOT,
                                       env=environment, stdout=log, stderr=subprocess.STDOUT)
            try:
                deadline = time.monotonic() + 30
                while True:
                    try:
                        with urllib.request.urlopen(base + "/api/status", context=context, timeout=2) as response:
                            state = json.load(response)
                        break
                    except (OSError, urllib.error.URLError):
                        if process.poll() is not None or time.monotonic() > deadline:
                            log.seek(0)
                            raise RuntimeError("Server failed to start:\n" + log.read())
                        time.sleep(.2)
                assert state["setup_required"] and not state["authenticated"], state
                yield base, temporary
            finally:
                stop_process_tree(process)


def main() -> None:
    with test_server() as (base, _):
        context = ssl._create_unverified_context()
        for path in ("/", "/static/app.js", "/static/app.css", "/openapi.json"):
            with urllib.request.urlopen(base + path, context=context, timeout=5) as response:
                assert response.status == 200, path
        print("PASS: HTTPS startup, first setup, static assets and OpenAPI")


if __name__ == "__main__":
    main()
