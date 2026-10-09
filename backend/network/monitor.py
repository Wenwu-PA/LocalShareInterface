from __future__ import annotations

import asyncio
import time

import psutil

from ..config import CONFIG
from ..models.database import connect, now_iso
from ..services.files import ROOT
from .scan import scan_devices

latest: dict = {"cpu": 0.0, "memory": 0.0, "disk_free": 0, "disk_total": 0,
                "bytes_sent_rate": 0, "bytes_recv_rate": 0, "active_transfers": 0,
                "sampled_at": time.time()}
_previous_net = None
_previous_interfaces: dict[str, tuple[float, int, int]] = {}
_last_persist = 0.0
_last_scan = 0.0


def _alert(db, kind: str, target: str, message: str) -> None:
    existing = db.execute("SELECT id FROM alerts WHERE type=? AND target=? AND resolved_at IS NULL", (kind, target)).fetchone()
    if not existing:
        db.execute("INSERT INTO alerts(occurred_at,type,target,message) VALUES(?,?,?,?)", (now_iso(), kind, target, message))


def collect() -> dict:
    global _previous_net, _previous_interfaces, _last_persist
    net = psutil.net_io_counters()
    now = time.monotonic()
    sent_rate = recv_rate = 0.0
    if _previous_net:
        elapsed = max(.001, now - _previous_net[0])
        sent_rate = max(0, net.bytes_sent - _previous_net[1]) / elapsed
        recv_rate = max(0, net.bytes_recv - _previous_net[2]) / elapsed
    _previous_net = (now, net.bytes_sent, net.bytes_recv)
    interface_rates = []
    for name, counters in psutil.net_io_counters(pernic=True).items():
        previous = _previous_interfaces.get(name)
        if previous:
            elapsed = max(.001, now - previous[0])
            sent = max(0, counters.bytes_sent - previous[1]) / elapsed
            received = max(0, counters.bytes_recv - previous[2]) / elapsed
        else:
            sent = received = 0
        interface_rates.append({"name": name, "bytes_sent_rate": int(sent), "bytes_recv_rate": int(received)})
        _previous_interfaces[name] = (now, counters.bytes_sent, counters.bytes_recv)
    disk = psutil.disk_usage(str(ROOT))
    with connect() as db:
        active_uploads = db.execute("SELECT COUNT(*) n FROM uploads WHERE status IN ('receiving','processing')").fetchone()["n"]
        active_downloads = db.execute("SELECT COUNT(*) n FROM transfers WHERE direction='download' AND status='active'").fetchone()["n"]
        latest.update({"cpu": float(psutil.cpu_percent(interval=None)), "memory": float(psutil.virtual_memory().percent),
                       "disk_free": int(disk.free), "disk_total": int(disk.total),
                       "bytes_sent_rate": int(sent_rate), "bytes_recv_rate": int(recv_rate),
                       "active_transfers": int(active_uploads + active_downloads), "interfaces": interface_rates,
                       "sampled_at": time.time()})
        if disk.free < 5 * 1024**3 or disk.percent >= 92:
            _alert(db, "disk_low", str(ROOT), f"Мало свободного места: {disk.free / 1024**3:.1f} ГБ")
        else:
            db.execute("UPDATE alerts SET resolved_at=? WHERE type='disk_low' AND resolved_at IS NULL", (now_iso(),))
        stalled = db.execute("""SELECT id,relative_path FROM uploads WHERE status='receiving'
                              AND julianday(updated_at)<julianday('now','-120 seconds')""").fetchall()
        stalled_ids = {row["id"] for row in stalled}
        for row in stalled:
            _alert(db, "transfer_slow", row["id"], f"Загрузка «{row['relative_path']}» не продвигалась более двух минут")
        if stalled_ids:
            placeholders = ",".join("?" for _ in stalled_ids)
            db.execute(f"UPDATE alerts SET resolved_at=? WHERE type='transfer_slow' AND resolved_at IS NULL AND target NOT IN ({placeholders})",
                       (now_iso(), *stalled_ids))
        else:
            db.execute("UPDATE alerts SET resolved_at=? WHERE type='transfer_slow' AND resolved_at IS NULL", (now_iso(),))
        if time.monotonic() - _last_persist >= 60:
            db.execute("INSERT OR REPLACE INTO metrics(occurred_at,cpu,memory,disk_free,bytes_sent,bytes_recv) VALUES(?,?,?,?,?,?)",
                       (now_iso(), latest["cpu"], latest["memory"], latest["disk_free"], net.bytes_sent, net.bytes_recv))
            days = max(1, min(int(CONFIG["monitoring"].get("history_days", 30)), 365))
            db.execute("DELETE FROM metrics WHERE julianday(occurred_at)<julianday('now',?)", (f"-{days} days",))
            db.execute("DELETE FROM alerts WHERE julianday(occurred_at)<julianday('now','-180 days')")
            db.execute("DELETE FROM audit_log WHERE julianday(occurred_at)<julianday('now','-365 days')")
            db.execute("DELETE FROM share_attempts WHERE julianday(attempted_at)<julianday('now','-60 days')")
            db.execute("DELETE FROM guest_sessions WHERE julianday(expires_at)<julianday('now')")
            _last_persist = time.monotonic()
    return dict(latest)


def scan_and_check() -> list[dict]:
    with connect() as db:
        prior_online = {row["ip"] for row in db.execute("SELECT ip FROM devices WHERE online=1")}
    items = scan_devices()
    online = {device["ip"] for device in items if device["online"]}
    with connect() as db:
        for device in items:
            if device["online"]:
                db.execute("UPDATE alerts SET resolved_at=? WHERE type='device_offline' AND target=? AND resolved_at IS NULL", (now_iso(), device["ip"]))
        for ip in prior_online - online:
            _alert(db, "device_offline", ip, f"Устройство {ip} пропало из локальной сети")
    return items


async def sampler() -> None:
    global _last_scan
    psutil.cpu_percent(interval=None)
    while True:
        interval = max(2, min(int(CONFIG["monitoring"].get("sample_seconds", 3)), 60))
        try:
            await asyncio.to_thread(collect)
            if time.monotonic() - _last_scan >= 30:
                await asyncio.to_thread(scan_and_check)
                _last_scan = time.monotonic()
        except Exception:
            import logging
            logging.getLogger("lanbridge.monitor").exception("Ошибка фонового мониторинга")
        await asyncio.sleep(interval)


def history(window: str) -> list[dict]:
    duration = {"hour": 1, "day": 24, "week": 168}.get(window)
    if duration is None:
        duration = 24
    with connect() as db:
        rows = db.execute("""SELECT occurred_at,cpu,memory,disk_free,bytes_sent,bytes_recv FROM metrics
                          WHERE julianday(occurred_at)>=julianday('now',?) ORDER BY occurred_at""",
                          (f"-{duration} hours",)).fetchall()
    return [dict(r) for r in rows]
