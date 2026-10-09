from __future__ import annotations

import secrets
from datetime import datetime, timedelta, timezone
from pathlib import Path

from argon2 import PasswordHasher, exceptions as argon_exceptions
from fastapi import HTTPException, Request, Response

from ..models.database import connect, now_iso
from ..security import digest
from .files import ROOT, safe_path

PASSWORDS = PasswordHasher()


def create_share(username: str, relative_path: str, ttl_hours: int, max_downloads: int, password: str | None) -> tuple[str, dict]:
    if not 1 <= ttl_hours <= 720:
        raise HTTPException(400, "Срок ссылки должен быть от 1 часа до 30 дней")
    if not 1 <= max_downloads <= 10000:
        raise HTTPException(400, "Лимит скачиваний должен быть от 1 до 10000")
    target = safe_path(relative_path, must_exist=True)
    if target.is_symlink() or target == ROOT:
        raise HTTPException(400, "Для общей ссылки выберите файл или папку")
    token = secrets.token_urlsafe(32)
    token_hash = digest(token)
    now = datetime.now(timezone.utc)
    expires = now + timedelta(hours=ttl_hours)
    password_hash = None
    if password:
        if len(password) < 8 or len(password) > 256:
            raise HTTPException(400, "Пароль ссылки должен содержать от 8 до 256 символов")
        password_hash = PASSWORDS.hash(password)
    with connect() as db:
        db.execute("""INSERT INTO shares(token_hash,relative_path,is_dir,created_by,created_at,expires_at,max_downloads,
                      downloads,password_hash,revoked) VALUES(?,?,?,?,?,?,?,0,?,0)""",
                   (token_hash, relative_path, int(target.is_dir()), username, now.isoformat(), expires.isoformat(), max_downloads, password_hash))
    return token, {"expires_at": expires.isoformat(), "max_downloads": max_downloads, "password_protected": bool(password_hash), "is_dir": target.is_dir()}


def get_share(token: str) -> dict:
    if len(token) < 32 or len(token) > 128:
        raise HTTPException(404, "Ссылка не найдена или истекла")
    with connect() as db:
        row = db.execute("SELECT * FROM shares WHERE token_hash=?", (digest(token),)).fetchone()
    if not row or row["revoked"] or row["expires_at"] <= now_iso() or row["downloads"] >= row["max_downloads"]:
        raise HTTPException(404, "Ссылка не найдена, истекла или исчерпала лимит")
    return dict(row)


def password_access(request: Request, share: dict) -> bool:
    if not share["password_hash"]:
        return True
    raw = request.cookies.get("lanbridge_guest")
    if not raw:
        return False
    with connect() as db:
        row = db.execute("SELECT share_hash,expires_at FROM guest_sessions WHERE token_hash=?", (digest(raw),)).fetchone()
    return bool(row and row["share_hash"] == share["token_hash"] and row["expires_at"] > now_iso())


def unlock_share(token: str, password: str, request: Request, response: Response) -> dict:
    share = get_share(token)
    ip = request.client.host if request.client else "unknown"
    with connect() as db:
        count = db.execute("""SELECT COUNT(*) n FROM share_attempts WHERE token_hash=? AND ip=?
                          AND succeeded=0 AND julianday(attempted_at)>=julianday('now','-15 minutes')""",
                           (share["token_hash"], ip)).fetchone()["n"]
        if count >= 8:
            raise HTTPException(429, "Слишком много попыток, попробуйте позже")
        db.execute("INSERT INTO share_attempts(token_hash,ip,attempted_at,succeeded) VALUES(?,?,?,0)",
                   (share["token_hash"], ip, now_iso()))
    try:
        valid = PASSWORDS.verify(share["password_hash"], password)
    except argon_exceptions.VerifyMismatchError:
        valid = False
    with connect() as db:
        db.execute("UPDATE share_attempts SET succeeded=? WHERE id=(SELECT MAX(id) FROM share_attempts WHERE token_hash=? AND ip=?)",
                   (int(valid), share["token_hash"], ip))
    if not valid:
        raise HTTPException(401, "Неверный пароль ссылки")
    guest = secrets.token_urlsafe(36)
    share_exp = datetime.fromisoformat(share["expires_at"])
    expiry = min(share_exp, datetime.now(timezone.utc) + timedelta(hours=12))
    with connect() as db:
        db.execute("INSERT INTO guest_sessions(token_hash,share_hash,expires_at,created_at,last_ip) VALUES(?,?,?,?,?)",
                   (digest(guest), share["token_hash"], expiry.isoformat(), now_iso(), ip))
    response.set_cookie("lanbridge_guest", guest, httponly=True, secure=request.url.scheme == "https",
                        samesite="strict", max_age=max(1, int((expiry - datetime.now(timezone.utc)).total_seconds())),
                        path=f"/api/public/{token}")
    return {"ok": True}


def share_listing(token: str, request: Request, subpath: str = "") -> dict:
    share = get_share(token)
    if not password_access(request, share):
        return {"password_required": True, "is_dir": share["is_dir"]}
    target = safe_path(share["relative_path"], must_exist=True)
    if target.is_symlink():
        raise HTTPException(404, "Файл не найден")
    if share["is_dir"]:
        from .files import list_directory
        prefix = share["relative_path"]
        if subpath:
            safe_subpath = subpath.replace("\\", "/")
            if safe_subpath.startswith("/") or any(part in {"", ".", ".."} for part in safe_subpath.split("/")):
                raise HTTPException(400, "Недопустимый путь")
            relative = prefix + "/" + safe_subpath
        else:
            relative = prefix
        current = safe_path(relative, must_exist=True)
        try:
            current.relative_to(target)
        except ValueError:
            raise HTTPException(400, "Путь выходит за пределы общей папки")
        if not current.is_dir():
            raise HTTPException(400, "Указанный путь не является папкой")
        items = list_directory(relative)
        for item in items:
            item["path"] = item["path"][len(prefix):].lstrip("/")
        return {"password_required": False, "is_dir": True, "name": target.name, "path": subpath, "items": items,
                "expires_at": share["expires_at"], "downloads": share["downloads"], "max_downloads": share["max_downloads"]}
    return {"password_required": False, "is_dir": False, "name": target.name,
            "size": target.stat().st_size, "mime": __import__('mimetypes').guess_type(target.name)[0] or "application/octet-stream",
            "expires_at": share["expires_at"], "downloads": share["downloads"], "max_downloads": share["max_downloads"]}


def shared_target(token: str, request: Request, subpath: str = "") -> tuple[dict, Path]:
    share = get_share(token)
    if not password_access(request, share):
        raise HTTPException(403, "Введите пароль ссылки")
    base = safe_path(share["relative_path"], must_exist=True)
    if share["is_dir"]:
        if not subpath:
            raise HTTPException(400, "Укажите файл для скачивания")
        normalized = subpath.replace("\\", "/")
        if normalized.startswith("/") or any(p in {"", ".", ".."} for p in normalized.split("/")):
            raise HTTPException(400, "Недопустимый путь")
        target = safe_path(share["relative_path"] + "/" + normalized, must_exist=True)
        try:
            target.relative_to(base)
        except ValueError:
            raise HTTPException(400, "Путь выходит за пределы общей папки")
    else:
        if subpath not in {"", base.name}:
            raise HTTPException(404, "Элемент не входит в общую ссылку")
        target = base
    if target.is_symlink() or (not target.is_file() and not target.is_dir()):
        raise HTTPException(404, "Файл не найден")
    with connect() as db:
        db.execute("""UPDATE shares SET downloads=downloads+1 WHERE token_hash=? AND revoked=0 AND downloads<max_downloads""",
                   (share["token_hash"],))
        if db.total_changes == 0:
            raise HTTPException(404, "Ссылка исчерпала лимит скачиваний")
    return share, target


def list_shares(username: str | None = None) -> list[dict]:
    with connect() as db:
        if username is None:
            rows = db.execute("""SELECT token_hash,relative_path,is_dir,created_by,created_at,expires_at,max_downloads,downloads,
                             CASE WHEN password_hash IS NULL THEN 0 ELSE 1 END password_protected,revoked
                             FROM shares ORDER BY created_at DESC LIMIT 200""").fetchall()
        else:
            rows = db.execute("""SELECT token_hash,relative_path,is_dir,created_by,created_at,expires_at,max_downloads,downloads,
                             CASE WHEN password_hash IS NULL THEN 0 ELSE 1 END password_protected,revoked
                             FROM shares WHERE created_by=? ORDER BY created_at DESC LIMIT 200""", (username,)).fetchall()
    result = []
    for row in rows:
        item = dict(row)
        item["active"] = not item["revoked"] and item["expires_at"] > now_iso() and item["downloads"] < item["max_downloads"]
        result.append(item)
    return result


def revoke_share(share_id: str) -> str:
    with connect() as db:
        row = db.execute("SELECT relative_path FROM shares WHERE token_hash=?", (share_id,)).fetchone()
        if not row:
            raise HTTPException(404, "Ссылка не найдена")
        db.execute("UPDATE shares SET revoked=1 WHERE token_hash=?", (share_id,))
        db.execute("DELETE FROM guest_sessions WHERE share_hash=?", (share_id,))
    return row["relative_path"]
