from __future__ import annotations

import hashlib
import os
import threading
import uuid
from datetime import datetime, timezone
from pathlib import Path

from fastapi import HTTPException, Request

from ..config import CONFIG
from ..models.database import connect, now_iso
from .files import STAGING, safe_path, validate_upload_path

_commit_lock = threading.Lock()


def _chunk_path(upload_id: str, index: int) -> Path:
    STAGING.mkdir(parents=True, exist_ok=True)
    return STAGING / f"{upload_id}.{index:08d}.part"


def _chunk_count(size: int, chunk_size: int) -> int:
    return (size + chunk_size - 1) // chunk_size


def start_upload(username: str, relative_path: str, size: int, modified: int, ip: str) -> dict:
    relative_path = validate_upload_path(relative_path)
    max_size = int(CONFIG["server"].get("max_upload_bytes", 1024**4))
    if size < 0 or size > max_size:
        raise HTTPException(413, f"Размер файла должен быть не больше {max_size} байт")
    chunk_size = max(256 * 1024, min(int(CONFIG["server"].get("chunk_size", 8 * 1024**2)), 64 * 1024**2))
    target = safe_path(relative_path)
    if target.exists():
        raise HTTPException(409, "Файл с таким именем уже существует. Переименуйте его или удалите предыдущую версию.")
    target.parent.mkdir(parents=True, exist_ok=True)
    with connect() as db:
        previous = db.execute("""SELECT id,chunk_size,status FROM uploads WHERE username=? AND relative_path=? AND total_size=? AND file_mtime=?
                                AND status IN ('receiving','paused') ORDER BY updated_at DESC LIMIT 1""",
                              (username, relative_path, size, modified)).fetchone()
        if previous:
            upload_id = previous["id"]
            chunks = [r["chunk_index"] for r in db.execute("SELECT chunk_index FROM upload_chunks WHERE upload_id=?", (upload_id,))]
            db.execute("UPDATE uploads SET status='receiving',updated_at=? WHERE id=?", (now_iso(), upload_id))
            db.execute("UPDATE transfers SET status='active' WHERE upload_id=?", (upload_id,))
            return {"upload_id": upload_id, "chunk_size": previous["chunk_size"], "received_chunks": chunks, "resumed": True}
        upload_id = uuid.uuid4().hex
        db.execute("""INSERT INTO uploads(id,username,relative_path,total_size,chunk_size,status,created_at,updated_at,file_mtime)
                      VALUES(?,?,?,?,?,'receiving',?,?,?)""",
                   (upload_id, username, relative_path, size, chunk_size, now_iso(), now_iso(), modified))
        db.execute("""INSERT INTO transfers(direction,username,relative_path,size,status,started_at,remote_ip,upload_id)
                      VALUES('upload',?,?,?,'active',?,?,?)""",
                   (username, relative_path, size, now_iso(), ip, upload_id))
    return {"upload_id": upload_id, "chunk_size": chunk_size, "received_chunks": [], "resumed": False}


def upload_info(upload_id: str, username: str) -> dict:
    with connect() as db:
        row = db.execute("SELECT * FROM uploads WHERE id=? AND username=?", (upload_id, username)).fetchone()
        if not row:
            raise HTTPException(404, "Сессия загрузки не найдена")
        chunks = [r["chunk_index"] for r in db.execute("SELECT chunk_index FROM upload_chunks WHERE upload_id=? ORDER BY chunk_index", (upload_id,))]
    return {"upload_id": row["id"], "path": row["relative_path"], "size": row["total_size"],
            "chunk_size": row["chunk_size"], "status": row["status"], "received_chunks": chunks}


async def save_chunk(upload_id: str, index: int, username: str, request: Request) -> dict:
    info = upload_info(upload_id, username)
    if info["status"] != "receiving":
        raise HTTPException(409, "Эта загрузка больше не принимает данные")
    total_chunks = _chunk_count(info["size"], info["chunk_size"])
    if index < 0 or index >= total_chunks:
        raise HTTPException(400, "Индекс чанка вне допустимого диапазона")
    expected = min(info["chunk_size"], info["size"] - index * info["chunk_size"])
    temp = STAGING / f"{upload_id}.{index:08d}.{uuid.uuid4().hex}.tmp"
    h = hashlib.sha256()
    received = 0
    STAGING.mkdir(parents=True, exist_ok=True)
    try:
        with temp.open("xb") as out:
            async for data in request.stream():
                if not data:
                    continue
                received += len(data)
                if received > expected:
                    raise HTTPException(413, "Чанк больше допустимого размера")
                out.write(data)
                h.update(data)
        if received != expected:
            raise HTTPException(400, f"Неполный чанк: ожидалось {expected} байт, получено {received}")
        chunk_hash = h.hexdigest()
        with connect() as db:
            existing = db.execute("SELECT size,sha256 FROM upload_chunks WHERE upload_id=? AND chunk_index=?", (upload_id, index)).fetchone()
            if existing:
                temp.unlink(missing_ok=True)
                if existing["size"] == received and existing["sha256"] == chunk_hash:
                    return {"ok": True, "received": received, "sha256": chunk_hash, "already_saved": True}
                raise HTTPException(409, "Этот чанк уже сохранён с другим содержимым")
            os.replace(temp, _chunk_path(upload_id, index))
            db.execute("INSERT INTO upload_chunks(upload_id,chunk_index,size,sha256) VALUES(?,?,?,?)",
                       (upload_id, index, received, chunk_hash))
            db.execute("UPDATE uploads SET updated_at=? WHERE id=?", (now_iso(), upload_id))
        return {"ok": True, "received": received, "sha256": chunk_hash, "already_saved": False}
    finally:
        temp.unlink(missing_ok=True)


def _assemble(upload_id: str, username: str) -> dict:
    with connect() as db:
        row = db.execute("SELECT * FROM uploads WHERE id=? AND username=?", (upload_id, username)).fetchone()
        if not row:
            raise HTTPException(404, "Сессия загрузки не найдена")
        if row["status"] == "complete":
            return {"ok": True, "path": row["relative_path"], "sha256": row["sha256"], "size": row["total_size"]}
        if row["status"] != "receiving":
            raise HTTPException(409, "Загрузка уже обрабатывается")
        count = _chunk_count(row["total_size"], row["chunk_size"])
        chunks = db.execute("SELECT chunk_index,size,sha256 FROM upload_chunks WHERE upload_id=? ORDER BY chunk_index", (upload_id,)).fetchall()
        if len(chunks) != count or [c["chunk_index"] for c in chunks] != list(range(count)):
            raise HTTPException(409, "Не все чанки получены; продолжите загрузку и повторите сборку")
        db.execute("UPDATE uploads SET status='processing',updated_at=? WHERE id=?", (now_iso(), upload_id))

    relative = row["relative_path"]
    target = safe_path(relative)
    target.parent.mkdir(parents=True, exist_ok=True)
    temp_target = target.parent / f".lanbridge-{uuid.uuid4().hex}.tmp"
    full_hash = hashlib.sha256()
    total = 0
    try:
        with temp_target.open("xb") as dest:
            for chunk in chunks:
                part = _chunk_path(upload_id, chunk["chunk_index"])
                part_hash = hashlib.sha256()
                part_size = 0
                with part.open("rb") as src:
                    while data := src.read(1024 * 1024):
                        dest.write(data)
                        part_hash.update(data)
                        full_hash.update(data)
                        part_size += len(data)
                        total += len(data)
                if part_size != chunk["size"] or part_hash.hexdigest() != chunk["sha256"]:
                    raise HTTPException(400, "Повреждённый чанк: контрольная сумма не совпала")
        if total != row["total_size"]:
            raise HTTPException(400, "Размер файла не совпал с объявленным")
        with _commit_lock:
            if target.exists():
                raise HTTPException(409, "Файл с таким именем уже появился")
            os.replace(temp_target, target)
        checksum = full_hash.hexdigest()
        ended = datetime.now(timezone.utc)
        with connect() as db:
            db.execute("UPDATE uploads SET status='complete',sha256=?,updated_at=? WHERE id=?", (checksum, now_iso(), upload_id))
            db.execute("UPDATE alerts SET resolved_at=? WHERE type='transfer_slow' AND target=? AND resolved_at IS NULL", (now_iso(), upload_id))
            transfer = db.execute("SELECT id,started_at,remote_ip FROM transfers WHERE upload_id=?", (upload_id,)).fetchone()
            elapsed = max(0.001, (ended - datetime.fromisoformat(transfer["started_at"])).total_seconds()) if transfer else None
            db.execute("""UPDATE transfers SET status='completed',sha256=?,completed_at=?,elapsed_seconds=?
                          WHERE upload_id=?""", (checksum, now_iso(), elapsed, upload_id))
            if transfer and transfer["remote_ip"]:
                db.execute("UPDATE devices SET bytes_in=bytes_in+? WHERE ip=?", (total, transfer["remote_ip"]))
        for chunk in chunks:
            _chunk_path(upload_id, chunk["chunk_index"]).unlink(missing_ok=True)
        return {"ok": True, "path": relative, "sha256": checksum, "size": total}
    except Exception:
        temp_target.unlink(missing_ok=True)
        with connect() as db:
            db.execute("UPDATE uploads SET status='receiving',updated_at=? WHERE id=? AND status='processing'", (now_iso(), upload_id))
        raise


async def complete_upload(upload_id: str, username: str) -> dict:
    import anyio
    return await anyio.to_thread.run_sync(_assemble, upload_id, username)


def cancel_upload(upload_id: str, username: str) -> dict:
    with connect() as db:
        row = db.execute("SELECT status FROM uploads WHERE id=? AND username=?", (upload_id, username)).fetchone()
        if not row:
            raise HTTPException(404, "Сессия загрузки не найдена")
        if row["status"] == "processing":
            raise HTTPException(409, "Загрузка уже собирается")
        db.execute("UPDATE uploads SET status='cancelled',updated_at=? WHERE id=?", (now_iso(), upload_id))
        db.execute("UPDATE transfers SET status='cancelled',completed_at=? WHERE upload_id=?", (now_iso(), upload_id))
    for part in STAGING.glob(f"{upload_id}.*"):
        part.unlink(missing_ok=True)
    return {"ok": True}


def pause_upload(upload_id: str, username: str, paused: bool) -> dict:
    target = "paused" if paused else "receiving"
    with connect() as db:
        row = db.execute("SELECT status FROM uploads WHERE id=? AND username=?", (upload_id, username)).fetchone()
        if not row:
            raise HTTPException(404, "Сессия загрузки не найдена")
        if row["status"] not in {"receiving", "paused"}:
            raise HTTPException(409, "Загрузка уже завершена или обрабатывается")
        db.execute("UPDATE uploads SET status=?,updated_at=? WHERE id=?", (target, now_iso(), upload_id))
        db.execute("UPDATE transfers SET status=? WHERE upload_id=?", ("paused" if paused else "active", upload_id))
    return {"ok": True, "status": target}
