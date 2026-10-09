"""Local-only monitoring, incident recovery and bounded load checks."""
from pathlib import Path
import argparse
import http.cookiejar
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
import secrets
import socket
import ssl
import subprocess
import tempfile
import threading
import time
import urllib.request

import psutil

ROOT = Path(__file__).resolve().parents[1]


class Target(BaseHTTPRequestHandler):
    healthy = True

    def do_GET(self):
        self.send_response(200 if self.healthy else 503)
        self.end_headers()
        self.wfile.write(b"ready")

    def log_message(self, *args):
        pass


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--monitors", type=int, default=50)
    parser.add_argument("--seconds", type=int, default=45)
    args = parser.parse_args()
    target = ThreadingHTTPServer(("127.0.0.1", 0), Target)
    threading.Thread(target=target.serve_forever, daemon=True).start()
    try:
        with tempfile.TemporaryDirectory(prefix="lanbridge-monitors-") as folder:
            root = Path(folder)
            with socket.socket() as probe:
                probe.bind(("127.0.0.1", 0))
                port = probe.getsockname()[1]
            config = root / "config.yaml"
            config.write_text(f"server:\n  port: {port}\n", encoding="utf-8")
            jar = http.cookiejar.CookieJar()
            opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar), urllib.request.HTTPSHandler(context=ssl._create_unverified_context()))
            base = f"https://127.0.0.1:{port}"

            def api(path, data=None):
                request = urllib.request.Request(base + "/api/" + path, data=None if data is None else json.dumps(data).encode())
                if data is not None:
                    request.add_header("Content-Type", "application/json")
                    request.add_header("X-CSRF-Token", next(c.value for c in jar if c.name == "__Host-lanbridge_go_csrf"))
                with opener.open(request, timeout=10) as response:
                    return json.load(response)

            env = {key: value for key, value in os.environ.items() if not key.startswith("LANBRIDGE_GO_")}
            with (root / "server.log").open("w", encoding="utf-8") as log:
                process = subprocess.Popen([str(ROOT / "dist/lanbridge.exe"), "-config", str(config)], stdout=log, stderr=log, env=env)
                try:
                    for _ in range(100):
                        try:
                            api("status")
                            break
                        except OSError:
                            time.sleep(.1)
                    api("setup", {"username": "monitoradmin", "password": secrets.token_urlsafe(24)})
                    definition = {"name": "Local target", "kind": "http", "target": f"http://127.0.0.1:{target.server_port}",
                                  "group": "LAN", "tags": "test", "interval": 5, "timeout": 1, "retries": 2,
                                  "latency_limit": 0, "expected_status": 200, "keyword": "ready", "method": "GET",
                                  "record": "A", "resolver": "", "certificate_days": 7, "paused": False, "order": 0}
                    first = api("monitors", definition)["id"]

                    def until(predicate, timeout=20):
                        deadline = time.monotonic() + timeout
                        while time.monotonic() < deadline:
                            value = predicate()
                            if value:
                                return value
                            time.sleep(.25)
                        raise AssertionError("Condition not reached")

                    until(lambda: api("monitors")[0]["state"]["status"] == "up")
                    Target.healthy = False
                    incident = until(lambda: next((i for i in api("incidents") if not i["closed"]), None))
                    Target.healthy = True
                    until(lambda: next((i for i in api("incidents") if i["id"] == incident["id"] and i["closed"]), None))
                    assert 0 < api("monitors/history?window=1h")["availability"] < 100
                    api("monitors/bulk", {"ids": [first], "action": "delete"})
                    for index in range(args.monitors):
                        api("monitors", {**definition, "name": f"Load {index}", "interval": 20})
                    until(lambda: len([m for m in api("monitors") if m["state"]["status"] == "up"]) == args.monitors)
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
                    assert len(api("monitors")) == args.monitors
                    print(json.dumps({"monitors": args.monitors, "interval_seconds": 20, "measurement_seconds": args.seconds,
                                      "working_set_peak_bytes": peak_rss, "private_peak_bytes": peak_private,
                                      "cpu_percent_one_core": round(cpu, 3)}, indent=2), flush=True)
                finally:
                    process.terminate()
                    process.wait(timeout=10)
            assert '"level":"ERROR"' not in (root / "server.log").read_text(), "Server logged errors"
        print("PASS: real HTTP up/down/recovery, retry threshold, incident, aggregates and monitor load", flush=True)
    finally:
        target.shutdown()
        target.server_close()


if __name__ == "__main__":
    main()
