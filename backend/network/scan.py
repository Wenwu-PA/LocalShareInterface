from __future__ import annotations

import platform
import re
import socket
import subprocess
import threading
from datetime import datetime, timezone

from ..models.database import connect, now_iso

MAC_RE = re.compile(r"(?:[0-9a-fA-F]{2}[:-]){5}[0-9a-fA-F]{2}")
IP_RE = re.compile(r"\b(?:\d{1,3}\.){3}\d{1,3}\b")
OUI = {
    "00:03:93": "Apple", "00:17:F2": "Apple", "00:1B:63": "Apple", "00:25:00": "Apple",
    "3C:22:FB": "Apple", "A4:83:E7": "Apple", "F0:18:98": "Apple", "F4:5C:89": "Apple",
    "00:1A:11": "Google", "3C:5A:B4": "Google", "F4:F5:D8": "Google",
    "00:1B:21": "Intel", "3C:97:0E": "Intel", "68:05:CA": "Intel",
    "00:12:FB": "Samsung", "34:23:BA": "Samsung", "E8:50:8B": "Samsung",
    "00:1D:7E": "ASUSTek", "2C:56:DC": "ASUSTek", "AC:9E:17": "TP-Link",
}
_hostname_lock = threading.Lock()
_hostname_cache: dict[str, str | None] = {}
_hostname_pending: set[str] = set()


def _mac_key(mac: str) -> str:
    return mac.upper().replace("-", ":")


def _vendor(mac: str) -> str:
    normalized = _mac_key(mac)
    return OUI.get(normalized[:8], "Неизвестен")


def _hostname(ip: str) -> str | None:
    with _hostname_lock:
        if ip in _hostname_cache:
            return _hostname_cache[ip]
        if ip not in _hostname_pending:
            _hostname_pending.add(ip)

            def lookup() -> None:
                try:
                    name = socket.gethostbyaddr(ip)[0]
                except (OSError, socket.herror):
                    name = None
                with _hostname_lock:
                    _hostname_cache[ip] = name
                    _hostname_pending.discard(ip)

            threading.Thread(target=lookup, name=f"lanbridge-dns-{ip}", daemon=True).start()
    return None


def read_arp_table() -> list[tuple[str, str]]:
    system = platform.system().lower()
    commands = [["arp", "-a"]] if system == "windows" else [["ip", "neigh", "show"], ["arp", "-an"]]
    for cmd in commands:
        try:
            raw = subprocess.run(cmd, capture_output=True, text=True, timeout=6, check=False)
        except (OSError, subprocess.TimeoutExpired):
            continue
        if raw.returncode not in (0, 1):
            continue
        found: dict[str, str] = {}
        for line in raw.stdout.splitlines():
            mac = MAC_RE.search(line)
            ip = IP_RE.search(line)
            if not mac or not ip:
                continue
            ip_text = ip.group(0)
            if ip_text.startswith("224.") or ip_text.startswith("239.") or ip_text.endswith(".255"):
                continue
            found[ip_text] = _mac_key(mac.group(0))
        if found:
            return list(found.items())
    return []


def scan_devices() -> list[dict]:
    now = now_iso()
    observed: dict[str, dict] = {}
    for ip, mac in read_arp_table():
        hostname = _hostname(ip)
        observed[ip] = {"ip": ip, "mac": mac, "vendor": _vendor(mac), "hostname": hostname}
    with connect() as db:
        for device in observed.values():
            old = db.execute("SELECT first_seen,hostname,bytes_in,bytes_out FROM devices WHERE ip=?", (device["ip"],)).fetchone()
            db.execute("""INSERT INTO devices(ip,mac,hostname,vendor,first_seen,last_seen,online)
                          VALUES(?,?,?,?,?,?,1) ON CONFLICT(ip) DO UPDATE SET
                          mac=excluded.mac,hostname=COALESCE(excluded.hostname,devices.hostname),
                          vendor=excluded.vendor,last_seen=excluded.last_seen,online=1""",
                       (device["ip"], device["mac"], device["hostname"], device["vendor"],
                        old["first_seen"] if old else now, now))
        existing = db.execute("SELECT ip FROM devices WHERE online=1").fetchall()
        visible = set(observed)
        # Devices seen through authenticated LANBridge traffic are also listed; retain them when
        # they are absent from the OS ARP cache for one scan to avoid false offline flaps.
        for row in existing:
            if row["ip"] not in visible and row["ip"] not in {"127.0.0.1", "::1"}:
                last = db.execute("SELECT last_seen FROM devices WHERE ip=?", (row["ip"],)).fetchone()["last_seen"]
                try:
                    age = (datetime.now(timezone.utc) - datetime.fromisoformat(last)).total_seconds()
                except (ValueError, TypeError):
                    age = 999
                if age > 90:
                    db.execute("UPDATE devices SET online=0 WHERE ip=?", (row["ip"],))
        rows = db.execute("SELECT ip,mac,hostname,vendor,first_seen,last_seen,online,bytes_in,bytes_out FROM devices ORDER BY online DESC,last_seen DESC").fetchall()
    return [dict(r) for r in rows]


def remember_client(ip: str) -> None:
    if ip in {"127.0.0.1", "::1", "unknown"} or ip.startswith("127."):
        return
    now = now_iso()
    with connect() as db:
        db.execute("""INSERT INTO devices(ip,hostname,vendor,first_seen,last_seen,online)
                      VALUES(?,NULL,'Неизвестен',?,?,1) ON CONFLICT(ip) DO UPDATE SET
                      last_seen=excluded.last_seen,online=1""", (ip, now, now))
