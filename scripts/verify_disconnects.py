"""Exercise browser navigation and abrupt HTTPS disconnects on disposable data."""
from __future__ import annotations

from pathlib import Path
import secrets
import socket
import ssl
import struct
import sys
import time
from urllib.parse import urlsplit

from playwright.sync_api import sync_playwright

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
from scripts.smoke import test_server  # noqa: E402
from scripts.verify_api import Client  # noqa: E402


def main() -> None:
    with test_server() as (base, temporary):
        (temporary / "files" / "sample.txt").write_text("connection regression", encoding="utf-8")
        password = secrets.token_urlsafe(24)
        client = Client(base)
        client.call("/")
        client.call("/api/setup", "POST", {"username": "disconnectcheck", "password": password})
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch()
            context = browser.new_context(ignore_https_errors=True)
            page = context.new_page()
            errors = []
            frames = []
            page.on("pageerror", lambda error: errors.append(str(error)))
            page.on("response", lambda response: errors.append(f"{response.status} {response.url}")
                    if response.url.startswith(base + "/api/") and response.status >= 400 else None)
            page.on("websocket", lambda ws: ws.on("framereceived", lambda data: frames.append(data)))
            page.goto(base)
            page.locator('[name="username"]').fill("disconnectcheck")
            page.locator('[name="password"]').fill(password)
            page.locator("#auth-submit").click()
            page.wait_for_url("**/app")
            for section, selector in (("devices", "#page-root h1"), ("monitoring", '[data-metric="cpu"]'),
                                      ("external", '[data-action="toggle-tunnel"]'), ("history", "#history-q"),
                                      ("settings", "#users-list"), ("files", "#file-rows tr")):
                page.locator(f'.nav-item[data-page="{section}"]').click()
                page.locator(selector).first.wait_for()
            for width in (1440, 390):
                page.set_viewport_size({"width": width, "height": 844})
                for _ in range(2):
                    page.locator("#theme-toggle").click()
                    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
            # Repeated navigation closes active TLS and WebSocket connections.
            for _ in range(8):
                page.reload()
                page.locator("#page-root h1").wait_for()
            page.wait_for_timeout(3500)
            assert frames, "No WebSocket monitoring updates received"
            browser.close()
            assert not errors, errors

        address = urlsplit(base)
        tls = ssl._create_unverified_context()
        cookie = "; ".join(f"{c.name}={c.value}" for c in client.cookies)
        for index in range(40):
            with socket.create_connection((address.hostname, address.port), timeout=5) as raw:
                with tls.wrap_socket(raw, server_hostname=address.hostname) as connection:
                    path = "/api/transfers?limit=200&q=&status=&direction=" if index % 2 else "/api/files?path="
                    connection.sendall((f"GET {path} HTTP/1.1\r\nHost: {address.netloc}\r\n"
                                        f"Cookie: {cookie}\r\nConnection: keep-alive\r\n\r\n").encode())
                    assert b"200 OK" in connection.recv(4096)
                    # Force TCP RST instead of an orderly TLS close notification.
                    linger = struct.pack("HH" if sys.platform == "win32" else "ii", 1, 0)
                    connection.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, linger)
            client.call("/api/files?path=&q=&sort=name&order=asc")
        time.sleep(1)
        client.call("/api/transfers?limit=200&q=&status=&direction=")
        log = (temporary / "server.log").read_text(encoding="utf-8")
        assert "Traceback" not in log and "ERROR" not in log, log
    print("PASS: desktop/mobile themes, six UI sections, WebSocket, eight reloads, 40 TCP resets; no server errors")


if __name__ == "__main__":
    main()
