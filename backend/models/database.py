from __future__ import annotations

import sqlite3
from contextlib import contextmanager
from datetime import datetime, timezone

from ..config import CONFIG


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


@contextmanager
def connect():
    db = sqlite3.connect(CONFIG["storage"]["database"], timeout=30)
    db.row_factory = sqlite3.Row
    db.execute("PRAGMA foreign_keys=ON")
    db.execute("PRAGMA journal_mode=WAL")
    try:
        yield db
        db.commit()
    except Exception:
        db.rollback()
        raise
    finally:
        db.close()


def initialize() -> None:
    with connect() as db:
        db.executescript("""
        CREATE TABLE IF NOT EXISTS users (
          id INTEGER PRIMARY KEY, username TEXT UNIQUE NOT NULL,
          password_hash TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'admin',
          created_at TEXT NOT NULL
        );
        CREATE TABLE IF NOT EXISTS sessions (
          token_hash TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
          csrf_hash TEXT NOT NULL, expires_at TEXT NOT NULL, created_at TEXT NOT NULL,
          last_ip TEXT
        );
        CREATE TABLE IF NOT EXISTS login_attempts (
          id INTEGER PRIMARY KEY, ip TEXT NOT NULL, attempted_at TEXT NOT NULL, succeeded INTEGER NOT NULL
        );
        CREATE TABLE IF NOT EXISTS uploads (
          id TEXT PRIMARY KEY, username TEXT NOT NULL, relative_path TEXT NOT NULL,
          total_size INTEGER NOT NULL, chunk_size INTEGER NOT NULL, status TEXT NOT NULL,
          created_at TEXT NOT NULL, updated_at TEXT NOT NULL, sha256 TEXT
        );
        CREATE TABLE IF NOT EXISTS upload_chunks (
          upload_id TEXT NOT NULL REFERENCES uploads(id) ON DELETE CASCADE,
          chunk_index INTEGER NOT NULL, size INTEGER NOT NULL, sha256 TEXT NOT NULL,
          PRIMARY KEY(upload_id, chunk_index)
        );
        CREATE TABLE IF NOT EXISTS transfers (
          id INTEGER PRIMARY KEY, direction TEXT NOT NULL, username TEXT, relative_path TEXT NOT NULL,
          size INTEGER NOT NULL, sha256 TEXT, status TEXT NOT NULL, started_at TEXT NOT NULL,
          completed_at TEXT, elapsed_seconds REAL, remote_ip TEXT
        );
        CREATE TABLE IF NOT EXISTS shares (
          token_hash TEXT PRIMARY KEY, relative_path TEXT NOT NULL, is_dir INTEGER NOT NULL,
          created_by TEXT NOT NULL, created_at TEXT NOT NULL, expires_at TEXT NOT NULL,
          max_downloads INTEGER NOT NULL, downloads INTEGER NOT NULL DEFAULT 0,
          password_hash TEXT, revoked INTEGER NOT NULL DEFAULT 0
        );
        CREATE TABLE IF NOT EXISTS guest_sessions (
          token_hash TEXT PRIMARY KEY, share_hash TEXT NOT NULL REFERENCES shares(token_hash) ON DELETE CASCADE,
          expires_at TEXT NOT NULL, created_at TEXT NOT NULL, last_ip TEXT
        );
        CREATE TABLE IF NOT EXISTS share_attempts (
          id INTEGER PRIMARY KEY, token_hash TEXT NOT NULL, ip TEXT NOT NULL, attempted_at TEXT NOT NULL,
          succeeded INTEGER NOT NULL
        );
        CREATE TABLE IF NOT EXISTS audit_log (
          id INTEGER PRIMARY KEY, occurred_at TEXT NOT NULL, actor TEXT, action TEXT NOT NULL,
          target TEXT, remote_ip TEXT, detail TEXT
        );
        CREATE TABLE IF NOT EXISTS devices (
          ip TEXT PRIMARY KEY, mac TEXT, hostname TEXT, vendor TEXT, first_seen TEXT NOT NULL,
          last_seen TEXT NOT NULL, online INTEGER NOT NULL DEFAULT 1, bytes_in INTEGER NOT NULL DEFAULT 0,
          bytes_out INTEGER NOT NULL DEFAULT 0
        );
        CREATE TABLE IF NOT EXISTS metrics (
          occurred_at TEXT NOT NULL, cpu REAL, memory REAL, disk_free INTEGER,
          bytes_sent INTEGER, bytes_recv INTEGER, PRIMARY KEY(occurred_at)
        );
        CREATE TABLE IF NOT EXISTS alerts (
          id INTEGER PRIMARY KEY, occurred_at TEXT NOT NULL, type TEXT NOT NULL,
          target TEXT NOT NULL DEFAULT '', message TEXT NOT NULL, resolved_at TEXT
        );
        CREATE INDEX IF NOT EXISTS idx_audit_time ON audit_log(occurred_at);
        CREATE INDEX IF NOT EXISTS idx_transfers_time ON transfers(started_at);
        CREATE INDEX IF NOT EXISTS idx_shares_expiry ON shares(expires_at);
        CREATE INDEX IF NOT EXISTS idx_alerts_time ON alerts(occurred_at);
        CREATE UNIQUE INDEX IF NOT EXISTS idx_alerts_open_unique ON alerts(type,target) WHERE resolved_at IS NULL;
        """)
        columns = {row["name"] for row in db.execute("PRAGMA table_info(transfers)")}
        if "upload_id" not in columns:
            db.execute("ALTER TABLE transfers ADD COLUMN upload_id TEXT")
        upload_columns = {row["name"] for row in db.execute("PRAGMA table_info(uploads)")}
        if "file_mtime" not in upload_columns:
            db.execute("ALTER TABLE uploads ADD COLUMN file_mtime INTEGER NOT NULL DEFAULT 0")
