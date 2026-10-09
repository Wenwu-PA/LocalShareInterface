from __future__ import annotations

import hashlib
import mimetypes
import re
import shutil
from pathlib import Path

from fastapi import HTTPException

from ..config import CONFIG

ROOT = CONFIG["storage"]["root"].resolve()
STAGING = ROOT / ".lanbridge-staging"
BLOCKED = {x.lower().lstrip(".") for x in CONFIG["security"].get("blocked_extensions", [])}
ALLOWED = {x.lower().lstrip(".") for x in CONFIG["security"].get("allowed_extensions", [])}
WINDOWS_RESERVED = {"CON", "PRN", "AUX", "NUL", *(f"COM{i}" for i in range(1, 10)), *(f"LPT{i}" for i in range(1, 10))}


def clean_component(value: str) -> str:
    value = value.strip()
    if not value or value in {".", ".."} or "\x00" in value or "/" in value or "\\" in value:
        raise HTTPException(400, "Недопустимое имя файла или папки")
    if value.endswith((".", " ")) or any(ord(c) < 32 for c in value) or any(c in value for c in '<>:"|?*'):
        raise HTTPException(400, "Имя содержит недопустимые символы")
    if value.startswith(".lanbridge-"):
        raise HTTPException(400, "Это имя зарезервировано LANBridge")
    if value.split(".")[0].upper() in WINDOWS_RESERVED:
        raise HTTPException(400, "Это имя зарезервировано системой")
    if len(value.encode("utf-8")) > 240:
        raise HTTPException(400, "Имя файла слишком длинное")
    return value


def clean_relative(value: str | None) -> str:
    if value is None or value in {"", "."}:
        return ""
    normalized = value.replace("\\", "/")
    if normalized.startswith("/") or re.match(r"^[a-zA-Z]:", normalized):
        raise HTTPException(400, "Абсолютные пути запрещены")
    parts = normalized.split("/")
    if any(part in {"", ".", ".."} for part in parts):
        raise HTTPException(400, "Недопустимый путь")
    return "/".join(clean_component(p) for p in parts)


def safe_path(relative: str | None, *, must_exist: bool = False) -> Path:
    clean = clean_relative(relative)
    candidate = ROOT.joinpath(*clean.split("/")) if clean else ROOT
    if candidate == STAGING or STAGING in candidate.parents:
        raise HTTPException(400, "Служебные файлы недоступны")
    current = ROOT
    for part in clean.split("/") if clean else []:
        current = current / part
        if current.is_symlink():
            raise HTTPException(400, "Символьные ссылки запрещены")
    try:
        resolved = candidate.resolve(strict=must_exist)
        resolved.relative_to(ROOT)
    except (ValueError, OSError):
        raise HTTPException(400, "Путь выходит за пределы общей папки")
    if must_exist and not resolved.exists():
        raise HTTPException(404, "Файл или папка не найдены")
    return resolved


def validate_upload_path(relative: str) -> str:
    clean = clean_relative(relative)
    if not clean:
        raise HTTPException(400, "Не указано имя файла")
    ext = Path(clean).suffix.lower().lstrip(".")
    if ext in BLOCKED or (ALLOWED and ext not in ALLOWED):
        raise HTTPException(415, "Этот тип файла запрещён настройками сервера")
    return clean


def list_directory(relative: str, query: str = "", sort: str = "name", order: str = "asc") -> list[dict]:
    base = safe_path(relative, must_exist=True)
    if not base.is_dir():
        raise HTTPException(400, "Указанный путь не является папкой")
    query = query.strip().casefold()
    items: list[dict] = []
    try:
        candidates = base.rglob("*") if query else base.iterdir()
        for path in candidates:
            if len(items) >= 2000:
                break
            if path.is_symlink() or STAGING in path.parents or path == STAGING or path.name.startswith(".lanbridge-"):
                continue
            try:
                rel = path.relative_to(ROOT).as_posix()
                stat = path.stat()
            except (OSError, ValueError):
                continue
            if query and query not in path.name.casefold() and query not in rel.casefold():
                continue
            is_dir = path.is_dir()
            mime = "inode/directory" if is_dir else mimetypes.guess_type(path.name)[0] or "application/octet-stream"
            items.append({"name": path.name, "path": rel, "is_dir": is_dir, "size": 0 if is_dir else stat.st_size,
                          "modified": stat.st_mtime, "mime": mime,
                          "preview": not is_dir and (mime.startswith(("image/", "video/")) or mime.startswith("text/"))})
    except PermissionError:
        raise HTTPException(403, "Нет доступа к этой папке")
    key = {"name": lambda x: x["name"].casefold(), "size": lambda x: x["size"],
           "modified": lambda x: x["modified"], "type": lambda x: x["mime"]}.get(sort, lambda x: x["name"].casefold())
    items.sort(key=key, reverse=(order == "desc"))
    items.sort(key=lambda x: not x["is_dir"])
    return items


def create_folder(relative: str) -> dict:
    target = safe_path(relative)
    if target.exists():
        raise HTTPException(409, "Такая папка уже существует")
    target.mkdir(parents=True, exist_ok=False)
    return {"path": target.relative_to(ROOT).as_posix(), "name": target.name}


def rename_path(relative: str, new_name: str) -> str:
    source = safe_path(relative, must_exist=True)
    if source == ROOT or source.is_symlink():
        raise HTTPException(400, "Нельзя переименовать этот путь")
    validate_upload_path(clean_component(new_name))
    destination = source.with_name(clean_component(new_name))
    safe_path(destination.relative_to(ROOT).as_posix())
    if destination.exists():
        raise HTTPException(409, "Элемент с таким именем уже существует")
    source.rename(destination)
    return destination.relative_to(ROOT).as_posix()


def delete_path(relative: str) -> None:
    target = safe_path(relative, must_exist=True)
    if target == ROOT or target.is_symlink() or STAGING in target.parents:
        raise HTTPException(400, "Нельзя удалить этот путь")
    if target.is_dir():
        shutil.rmtree(target)
    else:
        target.unlink()


def file_sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as src:
        while chunk := src.read(1024 * 1024):
            h.update(chunk)
    return h.hexdigest()
