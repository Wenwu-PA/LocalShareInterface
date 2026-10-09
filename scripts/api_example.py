"""Authenticate interactively, read files and metrics, then close the session."""
from __future__ import annotations

import argparse
import getpass
import http.cookiejar
import json
import ssl
import urllib.request


def run_example(base_url: str, username: str, password: str, *, self_signed: bool = False) -> dict:
    base = base_url.rstrip("/")
    context = ssl._create_unverified_context() if self_signed else ssl.create_default_context()
    jar = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar),
                                        urllib.request.HTTPSHandler(context=context))

    def call(path: str, method: str = "GET", body: dict | None = None):
        headers = {}
        data = json.dumps(body).encode("utf-8") if body is not None else None
        if data is not None:
            headers["Content-Type"] = "application/json"
        if method != "GET":
            headers["X-CSRF-Token"] = next(cookie.value for cookie in jar if cookie.name == "lanbridge_csrf")
        request = urllib.request.Request(base + path, data=data, headers=headers, method=method)
        with opener.open(request, timeout=15) as response:
            return json.load(response)

    with opener.open(base + "/", timeout=15) as response:
        response.read()
    call("/api/login", "POST", {"username": username, "password": password})
    try:
        me = call("/api/me")
        files = call("/api/files?path=")
        metrics = call("/api/metrics")
        return {"username": me["username"], "role": me["role"], "files": len(files["items"]),
                "cpu_percent": metrics["cpu"], "disk_free_bytes": metrics["disk_free"]}
    finally:
        call("/api/logout", "POST")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="https://localhost:8765")
    parser.add_argument("--self-signed", action="store_true", help="Accept the local self-signed certificate")
    args = parser.parse_args()
    result = run_example(args.base_url, input("Username: "), getpass.getpass("Password: "), self_signed=args.self_signed)
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
