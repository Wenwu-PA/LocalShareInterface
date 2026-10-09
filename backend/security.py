from __future__ import annotations

import hashlib
import hmac
import secrets
from datetime import datetime, timedelta, timezone
from typing import Optional

from argon2 import PasswordHasher
from fastapi import HTTPException, Request, Response

from .config import CONFIG
from .models.database import connect, now_iso

PASSWORDS = PasswordHasher()
COOKIE = "lanbridge_session"
CSRF_COOKIE = "lanbridge_csrf"


def digest(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()


def secure_cookie() -> bool:
    return bool(CONFIG["server"].get("https", True))


def issue_session(response: Response, username: str, ip: str) -> None:
    session = secrets.token_urlsafe(40)
    csrf = secrets.token_urlsafe(32)
    expires = datetime.now(timezone.utc) + timedelta(days=int(CONFIG["server"].get("session_days", 7)))
    with connect() as db:
        row = db.execute("SELECT id FROM users WHERE username=?", (username,)).fetchone()
        db.execute("INSERT INTO sessions VALUES(?,?,?,?,?,?)",
                   (digest(session), row["id"], digest(csrf), expires.isoformat(), now_iso(), ip))
    response.set_cookie(COOKIE, session, httponly=True, secure=secure_cookie(), samesite="strict",
                        max_age=int((expires - datetime.now(timezone.utc)).total_seconds()), path="/")
    response.set_cookie(CSRF_COOKIE, csrf, httponly=False, secure=secure_cookie(), samesite="strict",
                        max_age=int((expires - datetime.now(timezone.utc)).total_seconds()), path="/")


def clear_session(response: Response, request: Request) -> None:
    raw = request.cookies.get(COOKIE)
    if raw:
        with connect() as db:
            db.execute("DELETE FROM sessions WHERE token_hash=?", (digest(raw),))
    response.delete_cookie(COOKIE, path="/")
    response.delete_cookie(CSRF_COOKIE, path="/")


def current_user(request: Request, *, required: bool = True) -> Optional[dict]:
    raw = request.cookies.get(COOKIE)
    if not raw:
        if required:
            raise HTTPException(401, "Требуется войти в систему")
        return None
    with connect() as db:
        row = db.execute("""SELECT u.id,u.username,u.role,s.csrf_hash,s.expires_at
                            FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=?""",
                         (digest(raw),)).fetchone()
    if not row or row["expires_at"] < now_iso():
        if required:
            raise HTTPException(401, "Сессия истекла")
        return None
    if request.method in {"POST", "PUT", "PATCH", "DELETE"}:
        csrf_cookie = request.cookies.get(CSRF_COOKIE, "")
        csrf_header = request.headers.get("x-csrf-token", "")
        if not csrf_cookie or not csrf_header or not hmac.compare_digest(csrf_cookie, csrf_header) or not hmac.compare_digest(digest(csrf_cookie), row["csrf_hash"]):
            raise HTTPException(403, "Проверка CSRF не пройдена")
        origin = request.headers.get("origin")
        if origin and origin.rstrip("/") != str(request.base_url).rstrip("/"):
            raise HTTPException(403, "Запрос с неизвестного источника")
    if request.client:
        from .network.scan import remember_client
        remember_client(request.client.host)
    return {"id": row["id"], "username": row["username"], "role": row["role"]}


def require_admin(user: dict) -> dict:
    if user["role"] != "admin":
        raise HTTPException(403, "Нужны права администратора")
    return user


def add_audit(action: str, actor: str | None, target: str | None, ip: str | None, detail: str | None = None) -> None:
    with connect() as db:
        db.execute("INSERT INTO audit_log(occurred_at,actor,action,target,remote_ip,detail) VALUES(?,?,?,?,?,?)",
                   (now_iso(), actor, action, target, ip, detail))
