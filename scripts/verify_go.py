"""Smoke and short idle measurement of the Go binary against disposable data."""
from pathlib import Path
import argparse
import http.cookiejar
import json
import os
import secrets
import socket
import ssl
import subprocess
import tempfile
import time
import urllib.request

import psutil


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", default="dist/lanbridge.exe")
    parser.add_argument("--seconds", type=int, default=15)
    args = parser.parse_args()
    binary = Path(args.binary).resolve()
    with tempfile.TemporaryDirectory(prefix="lanbridge-go-") as folder:
        root = Path(folder)
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        config = root / "config.yaml"
        config.write_text(f"server:\n  host: 127.0.0.1\n  port: {port}\n", encoding="utf-8")
        environment = {key: value for key, value in os.environ.items() if not key.startswith("LANBRIDGE_GO_")}
        context = ssl._create_unverified_context()
        base = f"https://127.0.0.1:{port}"
        jar = http.cookiejar.CookieJar()
        opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar), urllib.request.HTTPSHandler(context=context))
        def call(path, body=None):
            data = None if body is None else json.dumps(body).encode()
            request = urllib.request.Request(base + path, data=data)
            if data:
                request.add_header("Content-Type", "application/json")
                request.add_header("X-CSRF-Token", next(c.value for c in jar if c.name == "__Host-lanbridge_go_csrf"))
            with opener.open(request, timeout=5) as response:
                return json.load(response)
        def start(log):
            process = subprocess.Popen([str(binary), "-config", str(config)], stdout=log, stderr=log, env=environment)
            for _ in range(100):
                try:
                    call("/healthz")
                    return process
                except OSError:
                    if process.poll() is not None:
                        raise RuntimeError((root / "server.log").read_text())
                    time.sleep(.1)
            process.terminate()
            process.wait(timeout=10)
            raise RuntimeError("Startup timeout")
        with (root / "server.log").open("w", encoding="utf-8") as log:
            process = start(log)
            try:
                assert call("/api/status")["setup_required"]
                call("/api/setup", {"username": "smokeadmin", "password": secrets.token_urlsafe(24)})
                assert call("/api/me")["role"] == "admin"
                observed = psutil.Process(process.pid)
                initial = observed.cpu_times()
                started = time.monotonic()
                peak_rss = peak_private = 0
                for _ in range(args.seconds):
                    time.sleep(1)
                    info = observed.memory_info()
                    peak_rss = max(peak_rss, info.rss)
                    peak_private = max(peak_private, getattr(info, "private", info.vms))
                final = observed.cpu_times()
                cpu = 100 * (final.user + final.system - initial.user - initial.system) / (time.monotonic() - started)
                print(json.dumps({"binary_bytes": binary.stat().st_size, "idle_seconds": args.seconds,
                                  "working_set_peak_bytes": peak_rss, "private_peak_bytes": peak_private,
                                  "cpu_percent_one_core": round(cpu, 3), "monitors": 0}, indent=2))
            finally:
                process.terminate()
                process.wait(timeout=10)
            # Deliberate hard stop verifies WAL recovery and session persistence.
            process = start(log)
            try:
                assert not call("/api/status")["setup_required"]
                assert call("/api/me")["username"] == "smokeadmin"
            finally:
                process.terminate()
                process.wait(timeout=10)
        assert '"level":"ERROR"' not in (root / "server.log").read_text(), "Server errors logged"
    print("PASS: HTTPS, setup, session persistence and recovery after forced process stop")


if __name__ == "__main__":
    main()
