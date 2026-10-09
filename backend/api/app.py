from __future__ import annotations

import asyncio
import ipaddress
import base64
import json
import logging
from logging.handlers import RotatingFileHandler
import mimetypes
import os
import re
import socket
import time
import zipfile
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import quote

import uvicorn
from argon2 import PasswordHasher, exceptions as argon_exceptions
from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.x509.oid import NameOID
from fastapi import FastAPI, HTTPException, Request, Response, WebSocket, WebSocketDisconnect
from fastapi.responses import FileResponse, JSONResponse, StreamingResponse
from fastapi.staticfiles import StaticFiles
from ..models.schemas import (Credentials, UploadStart, FileAction, RenameAction, ShareCreate, PasswordUnlock, TunnelToggle, UserCreate, PasswordChange, UploadPause)

from ..config import CONFIG, ROOT
from ..models.database import connect, initialize, now_iso
from ..security import COOKIE, add_audit, clear_session, current_user, digest, issue_session
from ..services.files import ROOT as FILE_ROOT, create_folder, delete_path, list_directory, rename_path, safe_path
from ..services.uploads import cancel_upload, complete_upload, pause_upload, save_chunk, start_upload, upload_info
from ..network.diagnostics import run_diagnostics
from ..network.asyncio_compat import install_windows_reset_cleanup
from ..network.monitor import collect as collect_metrics, history as metrics_history, latest as metrics_latest, sampler as monitor_sampler, scan_and_check
from ..services.shares import create_share, list_shares, revoke_share, share_listing, shared_target, unlock_share
from ..tunnels.providers import external_network_check, manager as tunnel_manager

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
logger = logging.getLogger("lanbridge")
passwords = PasswordHasher()
STATIC = ROOT / "frontend"

app = FastAPI(title="LANBridge", version=(ROOT / "VERSION").read_text(encoding="utf-8").strip(), description="Локальный обмен файлами и мониторинг сети")
app.mount("/static", StaticFiles(directory=STATIC), name="static")


def verify_pre_auth_csrf(request: Request) -> None:
    import hmac
    cookie = request.cookies.get("lanbridge_csrf", "")
    header = request.headers.get("x-csrf-token", "")
    if not cookie or not header or not hmac.compare_digest(cookie, header):
        raise HTTPException(403, "Проверка CSRF не пройдена")
    origin = request.headers.get("origin")
    if origin and origin.rstrip("/") != str(request.base_url).rstrip("/"):
        raise HTTPException(403, "Запрос с неизвестного источника")


@app.on_event("startup")
async def start_monitoring():
    install_windows_reset_cleanup()
    app.state.monitor_task = asyncio.create_task(monitor_sampler())


@app.on_event("shutdown")
async def stop_monitoring():
    task = getattr(app.state, "monitor_task", None)
    if task:
        task.cancel()
    await asyncio.to_thread(tunnel_manager.stop)


def remote_ip(request: Request) -> str:
    peer = request.client.host if request.client else "unknown"
    if tunnel_manager.is_trusted_proxy(peer, request.headers):
        forwarded = request.headers.get("cf-connecting-ip", "")
        try:
            return str(ipaddress.ip_address(forwarded))
        except ValueError:
            pass
    return peer


@app.get("/", include_in_schema=False)
async def index():
    return FileResponse(STATIC / "index.html")


@app.get("/app", include_in_schema=False)
async def protected_app(request: Request):
    current_user(request)
    return FileResponse(STATIC / "app.html")


@app.middleware("http")
async def csrf_bootstrap(request: Request, call_next):
    response = await call_next(request)
    if (request.url.path == "/" or request.url.path.startswith("/s/")) and not request.cookies.get("lanbridge_csrf"):
        import secrets
        response.set_cookie("lanbridge_csrf", secrets.token_urlsafe(32), httponly=False,
                            secure=bool(CONFIG["server"].get("https", True)), samesite="strict", path="/")
    return response


def is_local_peer(peer: str | None) -> bool:
    if not peer:
        return False
    try:
        ip = ipaddress.ip_address(peer)
        if isinstance(ip, ipaddress.IPv6Address) and ip.ipv4_mapped:
            ip = ip.ipv4_mapped
        return ip.is_private or ip.is_loopback or ip.is_link_local or ip in ipaddress.ip_network("100.64.0.0/10")
    except ValueError:
        return False


def external_route_label(path: str) -> str:
    if path.startswith("/api/public/"):
        tail = path.removeprefix("/api/public/").split("/")
        return "/api/public/[share]" + ("/" + "/".join(tail[1:]) if len(tail) > 1 else "")
    if path.startswith("/s/"):
        return "/s/[share]"
    return path


@app.middleware("http")
async def local_network_only(request: Request, call_next):
    peer = request.client.host if request.client else None
    via_tunnel = tunnel_manager.is_trusted_proxy(peer, request.headers)
    if not is_local_peer(peer) and not via_tunnel:
        return JSONResponse(status_code=403, content={"detail": "LANBridge принимает подключения только из локальной сети или активного туннеля"})
    response = await call_next(request)
    if via_tunnel:
        forwarded = request.headers.get("cf-connecting-ip", "")
        try:
            ipaddress.ip_address(forwarded)
        except ValueError:
            forwarded = "unknown"
        request_bytes = int(request.headers.get("content-length", "0") or 0)
        response_bytes = 0 if hasattr(response, "body_iterator") else int(response.headers.get("content-length", "0") or 0)
        tunnel_manager.record_request(request_bytes, response_bytes)
        add_audit("external_connection", "external", external_route_label(request.url.path), forwarded,
                  f"{request.method} {response.status_code}")
        if hasattr(response, "body_iterator"):
            original = response.body_iterator
            async def tracked_body():
                async for block in original:
                    size = len(block if isinstance(block, bytes) else block.encode("utf-8"))
                    tunnel_manager.providers[tunnel_manager.active_name].record_response_bytes(size)
                    yield block
            response.body_iterator = tracked_body()
    return response


@app.get("/api/status")
async def status(request: Request):
    with connect() as db:
        users = db.execute("SELECT COUNT(*) n FROM users").fetchone()["n"]
    user = current_user(request, required=False)
    return {"setup_required": users == 0, "authenticated": bool(user), "user": user}


def record_login(ip: str, success: bool) -> None:
    with connect() as db:
        db.execute("DELETE FROM login_attempts WHERE julianday(attempted_at) < julianday('now','-60 days')")
        db.execute("INSERT INTO login_attempts(ip,attempted_at,succeeded) VALUES(?,?,?)", (ip, now_iso(), int(success)))


def enforce_login_limit(ip: str) -> None:
    with connect() as db:
        count = db.execute("""SELECT COUNT(*) n FROM login_attempts WHERE ip=? AND succeeded=0
                            AND julianday(attempted_at) >= julianday('now', ?)""",
                           (ip, f"-{int(CONFIG['security'].get('login_window_minutes',15))} minutes")).fetchone()["n"]
    if count >= int(CONFIG["security"].get("login_attempts", 8)):
        raise HTTPException(429, "Слишком много попыток. Подождите и повторите вход.")


@app.post("/api/setup")
async def setup(body: Credentials, request: Request, response: Response):
    verify_pre_auth_csrf(request)
    with connect() as db:
        if db.execute("SELECT 1 FROM users LIMIT 1").fetchone():
            raise HTTPException(409, "Администратор уже создан")
        if len(body.password) < 12:
            raise HTTPException(400, "Пароль администратора должен содержать не менее 12 символов")
        db.execute("INSERT INTO users(username,password_hash,role,created_at) VALUES(?,?,?,?)",
                   (body.username, passwords.hash(body.password), "admin", now_iso()))
    ip = remote_ip(request)
    issue_session(response, body.username, ip)
    add_audit("setup_admin", body.username, None, ip)
    return {"ok": True, "user": {"username": body.username, "role": "admin"}}


@app.post("/api/login")
async def login(body: Credentials, request: Request, response: Response):
    verify_pre_auth_csrf(request)
    ip = remote_ip(request)
    enforce_login_limit(ip)
    with connect() as db:
        row = db.execute("SELECT username,password_hash,role FROM users WHERE username=?", (body.username,)).fetchone()
    ok = False
    if row:
        try:
            ok = passwords.verify(row["password_hash"], body.password)
        except argon_exceptions.VerifyMismatchError:
            pass
    record_login(ip, ok)
    if not ok:
        add_audit("login_failed", body.username, None, ip)
        with connect() as db:
            recent = db.execute("""SELECT COUNT(*) n FROM login_attempts WHERE ip=? AND succeeded=0
                                AND julianday(attempted_at)>=julianday('now','-15 minutes')""", (ip,)).fetchone()["n"]
            if recent >= 3:
                db.execute("INSERT OR IGNORE INTO alerts(occurred_at,type,target,message) VALUES(?,?,?,?)",
                           (now_iso(), "login_failed", ip, f"Несколько неудачных попыток входа с IP {ip}"))
        raise HTTPException(401, "Неверный логин или пароль")
    with connect() as db:
        db.execute("UPDATE alerts SET resolved_at=? WHERE type='login_failed' AND target=? AND resolved_at IS NULL", (now_iso(), ip))
    issue_session(response, row["username"], ip)
    add_audit("login", row["username"], None, ip)
    return {"ok": True, "user": {"username": row["username"], "role": row["role"]}}


@app.post("/api/logout")
async def logout(request: Request, response: Response):
    user = current_user(request)
    add_audit("logout", user["username"], None, remote_ip(request))
    clear_session(response, request)
    return {"ok": True}


@app.get("/api/me")
async def me(request: Request):
    return current_user(request)


@app.get("/api/users")
async def api_users(request: Request):
    user = current_user(request)
    if user["role"] != "admin":
        raise HTTPException(403, "Нужны права администратора")
    with connect() as db:
        rows = db.execute("SELECT username,role,created_at FROM users ORDER BY username").fetchall()
    return [dict(row) for row in rows]


@app.post("/api/users")
async def api_create_user(body: UserCreate, request: Request):
    admin = current_user(request)
    if admin["role"] != "admin":
        raise HTTPException(403, "Нужны права администратора")
    try:
        with connect() as db:
            db.execute("INSERT INTO users(username,password_hash,role,created_at) VALUES(?,?,?,?)",
                       (body.username, passwords.hash(body.password), "user", now_iso()))
    except Exception as exc:
        if "UNIQUE" in str(exc).upper():
            raise HTTPException(409, "Пользователь с таким именем уже есть")
        raise
    add_audit("user_create", admin["username"], body.username, remote_ip(request))
    return {"username": body.username, "role": "user"}


@app.delete("/api/users/{username}")
async def api_delete_user(username: str, request: Request):
    admin = current_user(request)
    if admin["role"] != "admin":
        raise HTTPException(403, "Нужны права администратора")
    if username == admin["username"]:
        raise HTTPException(400, "Нельзя удалить текущего администратора")
    with connect() as db:
        row = db.execute("SELECT role FROM users WHERE username=?", (username,)).fetchone()
        if not row:
            raise HTTPException(404, "Пользователь не найден")
        if row["role"] == "admin":
            raise HTTPException(403, "Другого администратора нельзя удалить отсюда")
        db.execute("DELETE FROM users WHERE username=?", (username,))
    add_audit("user_delete", admin["username"], username, remote_ip(request))
    return {"ok": True}


@app.post("/api/account/password")
async def api_change_password(body: PasswordChange, request: Request):
    user = current_user(request)
    with connect() as db:
        row = db.execute("SELECT password_hash FROM users WHERE id=?", (user["id"],)).fetchone()
        try:
            valid = passwords.verify(row["password_hash"], body.current_password)
        except argon_exceptions.VerifyMismatchError:
            valid = False
        if not valid:
            raise HTTPException(401, "Текущий пароль неверен")
        db.execute("UPDATE users SET password_hash=? WHERE id=?", (passwords.hash(body.new_password), user["id"]))
        db.execute("DELETE FROM sessions WHERE user_id=? AND token_hash<>?",
                   (user["id"], digest(request.cookies.get(COOKIE, ""))))
    add_audit("password_change", user["username"], None, remote_ip(request))
    return {"ok": True}


@app.get("/api/files")
async def api_files(request: Request, path: str = "", q: str = "", sort: str = "name", order: str = "asc"):
    current_user(request)
    return {"path": path, "items": list_directory(path, q, sort, order)}


@app.post("/api/files/folders")
async def api_create_folder(body: FileAction, request: Request):
    user = current_user(request)
    result = create_folder(body.path)
    add_audit("folder_create", user["username"], body.path, remote_ip(request))
    return result


@app.post("/api/files/rename")
async def api_rename(body: RenameAction, request: Request):
    user = current_user(request)
    new_path = rename_path(body.path, body.new_name)
    add_audit("file_rename", user["username"], new_path, remote_ip(request), body.path)
    return {"path": new_path}


@app.delete("/api/files")
async def api_delete(request: Request, path: str):
    user = current_user(request)
    delete_path(path)
    add_audit("file_delete", user["username"], path, remote_ip(request))
    return {"ok": True}


@app.post("/api/uploads/start")
async def api_start_upload(body: UploadStart, request: Request):
    user = current_user(request)
    return start_upload(user["username"], body.path, body.size, body.modified, remote_ip(request))


@app.get("/api/uploads/{upload_id}")
async def api_upload_info(upload_id: str, request: Request):
    user = current_user(request)
    return upload_info(upload_id, user["username"])


@app.post("/api/uploads/{upload_id}/pause")
async def api_pause_upload(upload_id: str, body: UploadPause, request: Request):
    user = current_user(request)
    return pause_upload(upload_id, user["username"], body.paused)


@app.put("/api/uploads/{upload_id}/chunks/{index}")
async def api_upload_chunk(upload_id: str, index: int, request: Request):
    user = current_user(request)
    return await save_chunk(upload_id, index, user["username"], request)


@app.post("/api/uploads/{upload_id}/complete")
async def api_complete_upload(upload_id: str, request: Request):
    user = current_user(request)
    result = await complete_upload(upload_id, user["username"])
    add_audit("file_upload", user["username"], result["path"], remote_ip(request),
              json.dumps({"size": result["size"], "sha256": result["sha256"]}))
    return result


@app.delete("/api/uploads/{upload_id}")
async def api_cancel_upload(upload_id: str, request: Request):
    user = current_user(request)
    result = cancel_upload(upload_id, user["username"])
    add_audit("upload_cancel", user["username"], upload_id, remote_ip(request))
    return result


def _safe_filename(filename: str) -> str:
    filename = filename.replace("\\", "/").split("/")[-1]
    ascii_name = re.sub(r"[^\x20-\x7e]", "_", filename).replace('"', "_") or "download"
    return f'attachment; filename="{ascii_name}"; filename*=UTF-8\'\'{quote(filename)}'


@app.get("/api/files/download")
async def api_download(request: Request, path: str):
    user = current_user(request)
    target = safe_path(path, must_exist=True)
    if target.is_symlink():
        raise HTTPException(400, "Символьные ссылки запрещены")
    if target.is_dir():
        temp_dir = FILE_ROOT / ".lanbridge-exports"
        temp_dir.mkdir(parents=True, exist_ok=True)
        archive = temp_dir / f"{int(time.time())}-{os.urandom(8).hex()}.zip"
        def make_zip():
            with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_STORED, allowZip64=True) as zf:
                for file in target.rglob("*"):
                    if file.is_file() and not file.is_symlink() and not any(part.startswith(".lanbridge-") for part in file.relative_to(FILE_ROOT).parts):
                        zf.write(file, file.relative_to(target.parent).as_posix())
        import anyio
        await anyio.to_thread.run_sync(make_zip)
        from starlette.background import BackgroundTask
        headers = {"Content-Disposition": _safe_filename(target.name + ".zip"), "X-Content-Type-Options": "nosniff"}
        return FileResponse(archive, media_type="application/zip", headers=headers,
                            background=BackgroundTask(lambda: archive.unlink(missing_ok=True)))
    if not target.is_file():
        raise HTTPException(404, "Это не файл")
    size = target.stat().st_size
    range_header = request.headers.get("range")
    start, end = 0, max(0, size - 1)
    status_code = 200
    if range_header:
        match = re.fullmatch(r"bytes=(\d*)-(\d*)", range_header.strip())
        if not match or size == 0:
            raise HTTPException(416, "Некорректный диапазон байтов", headers={"Content-Range": f"bytes */{size}"})
        left, right = match.groups()
        if left:
            start = int(left)
            end = min(int(right), size - 1) if right else size - 1
        elif right:
            start = max(0, size - int(right))
        else:
            raise HTTPException(416, "Некорректный диапазон байтов", headers={"Content-Range": f"bytes */{size}"})
        if start >= size or start > end:
            raise HTTPException(416, "Диапазон байтов вне файла", headers={"Content-Range": f"bytes */{size}"})
        status_code = 206
    length = end - start + 1 if size else 0
    with connect() as db:
        transfer_id = db.execute("""INSERT INTO transfers(direction,username,relative_path,size,status,started_at,remote_ip)
                            VALUES('download',?,?,?,'active',?,?)""",
                                 (user["username"], path, size, now_iso(), remote_ip(request))).lastrowid
    sent = 0
    def stream_file():
        nonlocal sent
        try:
            with target.open("rb") as source:
                source.seek(start)
                remaining = length
                while remaining:
                    block = source.read(min(1024 * 1024, remaining))
                    if not block:
                        break
                    sent += len(block)
                    remaining -= len(block)
                    yield block
        finally:
            with connect() as db:
                status = "completed" if sent == length else "interrupted"
                db.execute("UPDATE transfers SET status=?,completed_at=?,elapsed_seconds=? WHERE id=?",
                           (status, now_iso(), max(0.001, time.time() - started), transfer_id))
                if sent:
                    db.execute("UPDATE devices SET bytes_out=bytes_out+? WHERE ip=?", (sent, remote_ip(request)))
            if sent == length:
                add_audit("file_download", user["username"], path, remote_ip(request))
    started = time.time()
    headers = {"Accept-Ranges": "bytes", "Content-Length": str(length), "Content-Disposition": _safe_filename(target.name),
               "X-Content-Type-Options": "nosniff"}
    if status_code == 206:
        headers["Content-Range"] = f"bytes {start}-{end}/{size}"
    return StreamingResponse(stream_file(), status_code=status_code,
                             media_type=mimetypes.guess_type(target.name)[0] or "application/octet-stream", headers=headers)


@app.get("/api/files/preview")
async def api_preview(request: Request, path: str):
    current_user(request)
    target = safe_path(path, must_exist=True)
    if not target.is_file():
        raise HTTPException(400, "Предпросмотр доступен только для файлов")
    mime = mimetypes.guess_type(target.name)[0] or "application/octet-stream"
    ext = target.suffix.lower()
    safe_text = ext in {".txt", ".md", ".csv", ".json", ".log"}
    if not (mime.startswith(("image/", "video/")) or safe_text):
        raise HTTPException(415, "Предпросмотр этого типа файла не поддерживается")
    if safe_text:
        mime = "text/plain; charset=utf-8"
    return FileResponse(target, media_type=mime, headers={"Content-Disposition": "inline", "X-Content-Type-Options": "nosniff"})


@app.get("/api/transfers")
async def api_transfers(request: Request, limit: int = 100, offset: int = 0,
                        q: str = "", status: str = "", direction: str = ""):
    user = current_user(request)
    limit = max(1, min(limit, 500))
    clauses: list[str] = []
    params: list = []
    if user["role"] != "admin":
        clauses.append("username=?")
        params.append(user["username"])
    if q:
        clauses.append("relative_path LIKE ?")
        params.append(f"%{q[:100]}%")
    if status in {"active", "paused", "completed", "interrupted", "cancelled"}:
        clauses.append("status=?")
        params.append(status)
    if direction in {"upload", "download", "guest"}:
        clauses.append("direction=?")
        params.append(direction)
    where = " WHERE " + " AND ".join(clauses) if clauses else ""
    with connect() as db:
        rows = db.execute(f"""SELECT direction,username,relative_path,size,sha256,status,started_at,
                             completed_at,elapsed_seconds,remote_ip FROM transfers{where}
                             ORDER BY started_at DESC LIMIT ? OFFSET ?""",
                          (*params, limit, max(0, offset))).fetchall()
    return [dict(r) for r in rows]


@app.get("/api/devices")
async def api_devices(request: Request):
    current_user(request)
    with connect() as db:
        rows = db.execute("SELECT ip,mac,hostname,vendor,first_seen,last_seen,online,bytes_in,bytes_out FROM devices ORDER BY online DESC,last_seen DESC").fetchall()
    return {"items": [dict(r) for r in rows]}


@app.post("/api/devices/scan")
async def api_scan_devices(request: Request):
    user = current_user(request)
    if user["role"] != "admin":
        raise HTTPException(403, "Нужны права администратора")
    items = await asyncio.to_thread(scan_and_check)
    add_audit("network_scan", user["username"], None, remote_ip(request))
    return {"items": items}


@app.get("/api/metrics")
async def api_metrics(request: Request):
    current_user(request)
    if time.time() - metrics_latest.get("sampled_at", 0) > 5:
        await asyncio.to_thread(collect_metrics)
    return dict(metrics_latest)


@app.get("/api/metrics/history")
async def api_metrics_history(request: Request, window: str = "day"):
    current_user(request)
    return {"items": await asyncio.to_thread(metrics_history, window)}


@app.get("/api/alerts")
async def api_alerts(request: Request):
    current_user(request)
    with connect() as db:
        rows = db.execute("SELECT id,occurred_at,type,target,message,resolved_at FROM alerts ORDER BY occurred_at DESC LIMIT 100").fetchall()
    return [dict(r) for r in rows]


@app.get("/api/audit")
async def api_audit(request: Request, limit: int = 200, offset: int = 0, q: str = ""):
    user = current_user(request)
    if user["role"] != "admin":
        raise HTTPException(403, "Нужны права администратора")
    limit = max(1, min(limit, 500))
    term = f"%{q[:100]}%"
    with connect() as db:
        rows = db.execute("""SELECT occurred_at,actor,action,target,remote_ip,detail FROM audit_log
                          WHERE (?='' OR action LIKE ? OR actor LIKE ? OR target LIKE ?)
                          ORDER BY occurred_at DESC LIMIT ? OFFSET ?""",
                          (q, term, term, term, limit, max(0, offset))).fetchall()
    return [dict(r) for r in rows]


@app.get("/api/diagnostics")
async def api_diagnostics(request: Request):
    current_user(request)
    return await asyncio.to_thread(run_diagnostics)


@app.post("/api/diagnostics/speed")
async def api_speed_test(request: Request):
    current_user(request)
    started = time.perf_counter()
    total = 0
    async for block in request.stream():
        total += len(block)
        if total > 64 * 1024 * 1024:
            raise HTTPException(413, "Тест скорости ограничен 64 МиБ")
    elapsed = max(0.001, time.perf_counter() - started)
    add_audit("lan_speed_test", current_user(request)["username"], None, remote_ip(request), f"{total} bytes")
    return {"bytes": total, "seconds": elapsed, "bytes_per_second": total / elapsed}


async def websocket_session(websocket: WebSocket) -> dict | None:
    expected_scheme = "https" if websocket.url.scheme == "wss" else "http"
    if websocket.headers.get("origin") and websocket.headers["origin"].rstrip("/") != f"{expected_scheme}://{websocket.url.netloc}":
        return None
    raw = websocket.cookies.get(COOKIE)
    if not raw:
        return None
    with connect() as db:
        row = db.execute("""SELECT u.username,u.role,s.expires_at FROM sessions s JOIN users u ON u.id=s.user_id
                            WHERE s.token_hash=?""", (digest(raw),)).fetchone()
    if not row or row["expires_at"] < now_iso():
        return None
    return dict(row)


@app.websocket("/api/ws")
async def api_ws(websocket: WebSocket):
    peer = websocket.client.host if websocket.client else None
    via_tunnel = tunnel_manager.is_trusted_proxy(peer, websocket.headers)
    if not is_local_peer(peer) and not via_tunnel:
        await websocket.close(code=4403)
        return
    user = await websocket_session(websocket)
    if not user:
        await websocket.close(code=4401)
        return
    await websocket.accept()
    if via_tunnel:
        forwarded = websocket.headers.get("cf-connecting-ip", "unknown")
        tunnel_manager.record_request(0, 0)
        add_audit("external_connection", user["username"], "/api/ws", forwarded, "WEBSOCKET connected")
    with connect() as db:
        alert_cursor = db.execute("SELECT COALESCE(MAX(id),0) n FROM alerts").fetchone()["n"]
    try:
        while True:
            payload = dict(metrics_latest)
            with connect() as db:
                active = db.execute("""SELECT relative_path,size,status,direction FROM transfers WHERE status='active'
                                    UNION ALL SELECT relative_path,total_size,'active','upload' FROM uploads WHERE status IN ('receiving','processing')
                                    LIMIT 30""").fetchall()
                alerts = db.execute("SELECT id,type,message,occurred_at FROM alerts WHERE id>? ORDER BY id", (alert_cursor,)).fetchall()
            payload["transfers"] = [dict(x) for x in active]
            await websocket.send_json({"type": "metrics", **payload})
            for alert in alerts:
                alert_cursor = alert["id"]
                await websocket.send_json({"type": "alert", "message": alert["message"], "occurred_at": alert["occurred_at"]})
            await asyncio.sleep(3)
    except WebSocketDisconnect:
        return
    except Exception:
        logger.exception("WebSocket monitoring stream failed")


@app.get("/api/qr")
async def api_qr(request: Request):
    current_user(request)
    import qrcode
    from io import BytesIO
    image = qrcode.make(str(request.base_url).rstrip("/"))
    data = BytesIO()
    image.save(data, format="PNG")
    return {"data_url": "data:image/png;base64," + base64.b64encode(data.getvalue()).decode("ascii"), "url": str(request.base_url).rstrip("/")}


@app.get("/api/external")
async def api_external(request: Request):
    user = current_user(request)
    if user["role"] != "admin":
        raise HTTPException(403, "Нужны права администратора")
    status = tunnel_manager.status()
    status["providers"] = [
        {"name": "cloudflare", "label": "Cloudflare Quick Tunnel", "ready": tunnel_manager.providers["cloudflare"].available(),
         "description": "Временный HTTPS-адрес; требуется установленный cloudflared. Предназначен для демонстраций."},
        {"name": "netbird", "label": "NetBird", "ready": False,
         "description": "Подключение к уже существующей VPN-сети NetBird требует отдельной настройки."},
        {"name": "reverse_proxy", "label": "Обратный прокси", "ready": False,
         "description": "Нужны свой домен, TLS и управляемое правило firewall."},
    ]
    return status


@app.post("/api/external/toggle")
async def api_external_toggle(body: TunnelToggle, request: Request):
    user = current_user(request)
    if user["role"] != "admin":
        raise HTTPException(403, "Нужны права администратора")
    if body.enabled:
        try:
            status = await asyncio.to_thread(tunnel_manager.start)
        except Exception as exc:
            provider = tunnel_manager.providers.get(tunnel_manager.active_name)
            if hasattr(provider, "error"):
                provider.error = str(exc)[:300]
            add_audit("tunnel_enable_failed", user["username"], tunnel_manager.active_name, remote_ip(request), str(exc)[:200])
            status = tunnel_manager.status()
            status["status"] = "Ошибка"
            status["error"] = str(exc)
            return status
        add_audit("tunnel_enabled", user["username"], status.get("url"), remote_ip(request))
        return status
    status = await asyncio.to_thread(tunnel_manager.stop)
    add_audit("tunnel_disabled", user["username"], None, remote_ip(request))
    return status


@app.get("/api/external/check")
async def api_external_check(request: Request):
    user = current_user(request)
    if user["role"] != "admin":
        raise HTTPException(403, "Нужны права администратора")
    result = await asyncio.to_thread(external_network_check, int(CONFIG["server"].get("port", 8765)))
    add_audit("external_network_check", user["username"], str(result.get("public_ip") or "unknown"), remote_ip(request))
    return result


@app.get("/api/shares")
async def api_list_shares(request: Request):
    user = current_user(request)
    return {"items": list_shares(None if user["role"] == "admin" else user["username"])}


@app.post("/api/shares")
async def api_create_share(body: ShareCreate, request: Request):
    user = current_user(request)
    token, metadata = create_share(user["username"], body.path, body.ttl_hours, body.max_downloads, body.password)
    add_audit("share_create", user["username"], body.path, remote_ip(request),
              json.dumps({"expires_at": metadata["expires_at"], "max_downloads": body.max_downloads, "password_protected": metadata["password_protected"]}))
    return {"url": f"{str(request.base_url).rstrip('/')}/s/{token}", "token_hash": digest(token), **metadata}


@app.delete("/api/shares/{share_id}")
async def api_revoke_share(share_id: str, request: Request):
    user = current_user(request)
    if user["role"] != "admin":
        raise HTTPException(403, "Нужны права администратора")
    target = revoke_share(share_id)
    add_audit("share_revoke", user["username"], target, remote_ip(request))
    return {"ok": True}


@app.get("/s/{token}", include_in_schema=False)
async def share_page(token: str):
    return FileResponse(STATIC / "guest.html")


@app.get("/api/public/{token}")
async def public_share_info(token: str, request: Request, path: str = ""):
    share = share_listing(token, request, path)
    if not share["password_required"]:
        row = __import__("backend.services.shares", fromlist=["get_share"]).get_share(token)
        add_audit("guest_access", "guest", row["relative_path"], remote_ip(request), f"share by {row['created_by']}")
    return share


@app.post("/api/public/{token}/unlock")
async def public_unlock(token: str, body: PasswordUnlock, request: Request, response: Response):
    verify_pre_auth_csrf(request)
    return unlock_share(token, body.password, request, response)


@app.get("/api/public/{token}/download")
async def public_download(token: str, request: Request, path: str = ""):
    share, target = shared_target(token, request, path)
    if target.is_dir():
        archive_dir = FILE_ROOT / ".lanbridge-exports"
        archive_dir.mkdir(parents=True, exist_ok=True)
        archive = archive_dir / f"shared-{os.urandom(12).hex()}.zip"
        def make_zip():
            with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_STORED, allowZip64=True) as zf:
                for file in target.rglob("*"):
                    if file.is_file() and not file.is_symlink() and not any(part.startswith(".lanbridge-") for part in file.relative_to(FILE_ROOT).parts):
                        zf.write(file, file.relative_to(target.parent).as_posix())
        import anyio
        await anyio.to_thread.run_sync(make_zip)
        from starlette.background import BackgroundTask
        add_audit("guest_download", "guest", share["relative_path"], remote_ip(request), f"share by {share['created_by']}")
        return FileResponse(archive, media_type="application/zip", headers={"Content-Disposition": _safe_filename(target.name + ".zip")},
                            background=BackgroundTask(lambda: archive.unlink(missing_ok=True)))
    size = target.stat().st_size
    range_header = request.headers.get("range")
    start, end, status_code = 0, max(0, size - 1), 200
    if range_header:
        match = re.fullmatch(r"bytes=(\d*)-(\d*)", range_header.strip())
        if not match or not size:
            raise HTTPException(416, "Некорректный диапазон", headers={"Content-Range": f"bytes */{size}"})
        left, right = match.groups()
        start = int(left) if left else max(0, size - int(right or 0))
        end = min(int(right), size - 1) if left and right else (size - 1 if left else size - 1)
        if start >= size or start > end:
            raise HTTPException(416, "Диапазон вне файла", headers={"Content-Range": f"bytes */{size}"})
        status_code = 206
    length = end - start + 1 if size else 0
    with connect() as db:
        transfer_id = db.execute("""INSERT INTO transfers(direction,username,relative_path,size,status,started_at,remote_ip)
                         VALUES('guest',?,?,?,'active',?,?)""", ("guest", share["relative_path"], size, now_iso(), remote_ip(request))).lastrowid
    sent = 0
    started = time.time()
    def stream_shared():
        nonlocal sent
        try:
            with target.open("rb") as source:
                source.seek(start)
                remaining = length
                while remaining:
                    block = source.read(min(1024 * 1024, remaining))
                    if not block:
                        break
                    sent += len(block)
                    remaining -= len(block)
                    yield block
        finally:
            with connect() as db:
                db.execute("UPDATE transfers SET status=?,completed_at=?,elapsed_seconds=? WHERE id=?",
                           ("completed" if sent == length else "interrupted", now_iso(), max(.001, time.time() - started), transfer_id))
            if sent == length:
                add_audit("guest_download", "guest", share["relative_path"], remote_ip(request), f"share by {share['created_by']}")
    headers = {"Accept-Ranges": "bytes", "Content-Length": str(length), "Content-Disposition": _safe_filename(target.name), "X-Content-Type-Options": "nosniff"}
    if status_code == 206:
        headers["Content-Range"] = f"bytes {start}-{end}/{size}"
    return StreamingResponse(stream_shared(), status_code=status_code,
                             media_type=mimetypes.guess_type(target.name)[0] or "application/octet-stream", headers=headers)


def _local_ips() -> list[str]:
    found = {"127.0.0.1"}
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        try:
            sock.connect(("192.0.2.1", 80))
            found.add(sock.getsockname()[0])
        finally:
            sock.close()
    except OSError:
        pass
    return sorted(found)


def ensure_tls() -> tuple[Path, Path]:
    directory: Path = CONFIG["storage"]["tls_dir"]
    directory.mkdir(parents=True, exist_ok=True)
    cert_path, key_path = directory / "lanbridge.crt", directory / "lanbridge.key"
    if cert_path.exists() and key_path.exists():
        return cert_path, key_path
    key = ec.generate_private_key(ec.SECP256R1())
    subject = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "LANBridge local server")])
    now = datetime.now(timezone.utc)
    san = [x509.DNSName("localhost"), *(x509.IPAddress(ipaddress.ip_address(ip)) for ip in _local_ips())]
    cert = (x509.CertificateBuilder().subject_name(subject).issuer_name(subject).public_key(key.public_key())
            .serial_number(x509.random_serial_number()).not_valid_before(now).not_valid_after(now.replace(year=now.year + 5))
            .add_extension(x509.SubjectAlternativeName(san), critical=False).add_extension(x509.BasicConstraints(ca=False, path_length=None), critical=True)
            .sign(key, hashes.SHA256()))
    key_path.write_bytes(key.private_bytes(serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8, serialization.NoEncryption()))
    cert_path.write_bytes(cert.public_bytes(serialization.Encoding.PEM))
    try:
        os.chmod(key_path, 0o600)
    except OSError:
        pass
    return cert_path, key_path


def main(*, reload: bool = False) -> None:
    CONFIG["storage"]["root"].mkdir(parents=True, exist_ok=True)
    CONFIG["storage"]["database"].parent.mkdir(parents=True, exist_ok=True)
    CONFIG["storage"]["logs"].mkdir(parents=True, exist_ok=True)
    initialize()
    file_handler = RotatingFileHandler(CONFIG["storage"]["logs"] / "lanbridge.log", maxBytes=5 * 1024 * 1024,
                                       backupCount=5, encoding="utf-8")
    file_handler.setFormatter(logging.Formatter("%(asctime)s %(levelname)s %(name)s %(message)s"))
    logging.getLogger().addHandler(file_handler)
    options = {"app": "backend.api.app:app", "host": CONFIG["server"].get("host", "0.0.0.0"),
               "port": int(CONFIG["server"].get("port", 8765)), "reload": reload, "log_level": "info",
               "proxy_headers": False}
    if reload:
        options["reload_dirs"] = [str(ROOT / "backend")]
    if CONFIG["server"].get("https", True):
        custom_cert = CONFIG["server"].get("tls_certfile")
        custom_key = CONFIG["server"].get("tls_keyfile")
        if bool(custom_cert) != bool(custom_key):
            raise RuntimeError("Для собственного HTTPS-сертификата задайте и tls_certfile, и tls_keyfile")
        if custom_cert and custom_key:
            cert, key = Path(custom_cert), Path(custom_key)
            if not cert.is_file() or not key.is_file():
                raise RuntimeError("Файл tls_certfile или tls_keyfile не найден")
        else:
            cert, key = ensure_tls()
        options.update(ssl_certfile=str(cert), ssl_keyfile=str(key))
        scheme = "https"
    else:
        scheme = "http"
    logger.info("LANBridge запущен: %s://localhost:%s", scheme, options["port"])
    uvicorn.run(**options)
