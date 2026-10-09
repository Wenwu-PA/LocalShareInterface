"""Exercise critical HTTP flows against a real, disposable HTTPS server."""
from __future__ import annotations

import hashlib
import http.cookiejar
import json
from pathlib import Path
import secrets
import sqlite3
import ssl
import sys
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
from scripts.api_example import run_example  # noqa: E402
from scripts.smoke import test_server  # noqa: E402


class Client:
    def __init__(self, base: str):
        self.base = base
        self.cookies = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.cookies),
                                                 urllib.request.HTTPSHandler(context=ssl._create_unverified_context()))

    def call(self, path: str, method: str = "GET", body=None, *, expected: int = 200,
             headers: dict | None = None, csrf: bool = True):
        headers = dict(headers or {})
        if isinstance(body, dict):
            body = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        if method != "GET" and csrf:
            headers["X-CSRF-Token"] = next((c.value for c in self.cookies if c.name == "lanbridge_csrf"), "")
        request = urllib.request.Request(self.base + path, data=body, headers=headers, method=method)
        try:
            response = self.opener.open(request, timeout=20)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            payload = response.read()
            assert response.status == expected, (path, response.status, payload[:200])
            return json.loads(payload) if "application/json" in response.headers.get("Content-Type", "") else payload


def main() -> None:
    with test_server() as (base, temporary):
        admin = Client(base)
        admin.call("/")
        password = secrets.token_urlsafe(24)
        admin.call("/api/setup", "POST", {"username": "smokeadmin", "password": password})
        assert run_example(base, "smokeadmin", password, self_signed=True)["role"] == "admin"
        admin.call("/api/files/folders", "POST", {"path": "forbidden"}, csrf=False, expected=403)
        admin.call("/api/files?path=..%2F", expected=400)
        payload = b"LANBridge" * 932068  # Slightly more than the default 8 MiB chunk.
        metadata = {"path": "resume.bin", "size": len(payload), "modified": 123}
        upload = admin.call("/api/uploads/start", "POST", metadata)
        prefix = f"/api/uploads/{upload['upload_id']}"
        size = upload["chunk_size"]
        admin.call(prefix + "/chunks/0", "PUT", payload[:size], headers={"Content-Type": "application/octet-stream"})
        admin.call(prefix + "/pause", "POST", {"paused": True})
        resumed = admin.call("/api/uploads/start", "POST", metadata)
        assert resumed["upload_id"] == upload["upload_id"] and resumed["received_chunks"] == [0]
        for index in range(1, (len(payload) + size - 1) // size):
            admin.call(prefix + f"/chunks/{index}", "PUT", payload[index * size:(index + 1) * size],
                       headers={"Content-Type": "application/octet-stream"})
        completed = admin.call(prefix + "/complete", "POST")
        assert completed["sha256"] == hashlib.sha256(payload).hexdigest()
        assert admin.call("/api/files/download?path=resume.bin", headers={"Range": "bytes=0-99"}, expected=206) == payload[:100]
        share_password = secrets.token_urlsafe(16)
        share = admin.call("/api/shares", "POST", {"path": "resume.bin", "ttl_hours": 1, "max_downloads": 3,
                                                     "password": share_password})
        token = share["url"].rsplit("/", 1)[1]
        guest = Client(base)
        guest.call("/s/" + token)
        assert guest.call("/api/public/" + token)["password_required"]
        guest.call(f"/api/public/{token}/unlock", "POST", {"password": share_password})
        assert guest.call(f"/api/public/{token}/download", headers={"Range": "bytes=10-19"}, expected=206) == payload[10:20]
        admin.call("/api/shares/" + share["token_hash"], "DELETE")
        guest.call("/api/public/" + token, expected=404)
        expired = admin.call("/api/shares", "POST", {"path": "resume.bin", "ttl_hours": 1, "max_downloads": 1})
        with sqlite3.connect(temporary / "test.sqlite3") as database:
            database.execute("UPDATE shares SET expires_at='2000-01-01T00:00:00+00:00' WHERE token_hash=?", (expired["token_hash"],))
        database.close()
        guest.call("/api/public/" + expired["url"].rsplit("/", 1)[1], expected=404)
        user_password = secrets.token_urlsafe(24)
        admin.call("/api/users", "POST", {"username": "smokeuser", "password": user_password})
        user = Client(base)
        user.call("/")
        user.call("/api/login", "POST", {"username": "smokeuser", "password": user_password})
        assert user.call("/api/transfers") == []
        user.call("/api/audit", expected=403)
        user.call("/api/external/toggle", "POST", {"enabled": True}, expected=403)
        tunnel = admin.call("/api/external/toggle", "POST", {"enabled": True})
        assert not tunnel["enabled"] and tunnel["error"]
        admin.call("/api/users/smokeuser", "DELETE")
        user.call("/api/me", expected=401)
        admin.call("/api/metrics")
        admin.call("/api/metrics/history?window=week")
        log = (temporary / "server.log").read_text(encoding="utf-8")
        assert "Traceback" not in log and "ERROR" not in log, log
    print("PASS: API example, auth/CSRF/roles, traversal, resume/SHA-256/Range, guest password/expiry/revoke, tunnel guard")


if __name__ == "__main__":
    main()
