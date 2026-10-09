from __future__ import annotations

import hashlib
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from fastapi import HTTPException
from starlette.requests import Request

from backend.models import database as db
from backend.config import CONFIG
from backend.services import files, uploads


class SafePathTests(unittest.TestCase):
    def test_rejects_absolute_parent_and_windows_reserved_paths(self):
        for value in ("../secret.txt", "a/../../secret", "C:/Windows/win.ini", "NUL.txt", "folder\\..\\secret"):
            with self.subTest(value=value), self.assertRaises(HTTPException):
                files.clean_relative(value)

    def test_resolves_only_inside_share_and_refuses_symlinks(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp) / "share"
            base.mkdir()
            outside = Path(tmp) / "outside.txt"
            outside.write_text("secret", encoding="utf-8")
            (base / "inside.txt").write_text("safe", encoding="utf-8")
            try:
                (base / "link.txt").symlink_to(outside)
            except OSError as exc:
                self.skipTest(f"Symbolic links are unavailable: {exc}")
            with patch.object(files, "ROOT", base.resolve()), patch.object(files, "STAGING", base / ".lanbridge-staging"):
                self.assertEqual(files.safe_path("inside.txt", must_exist=True).read_text(encoding="utf-8"), "safe")
                with self.assertRaises(HTTPException):
                    files.safe_path("../outside.txt")
                with self.assertRaises(HTTPException):
                    files.safe_path("link.txt", must_exist=True)

    def test_disallowed_executable_extension(self):
        with self.assertRaises(HTTPException):
            files.validate_upload_path("subfolder/run.exe")
        self.assertEqual(files.validate_upload_path("subfolder/notes.txt"), "subfolder/notes.txt")


class ChunkedUploadTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.base = Path(self.tmp.name) / "share"
        self.base.mkdir()
        self.stage = self.base / ".lanbridge-staging"
        self.database = Path(self.tmp.name) / "test.sqlite3"
        self.patchers = [
            patch.dict(CONFIG["storage"], {"root": self.base, "database": self.database}),
            patch.dict(CONFIG["server"], {"chunk_size": 256 * 1024, "max_upload_bytes": 5 * 1024 * 1024}),
            patch.object(files, "ROOT", self.base),
            patch.object(files, "STAGING", self.stage),
            patch.object(uploads, "STAGING", self.stage),
        ]
        for p in self.patchers:
            p.start()
        db.initialize()

    async def asyncTearDown(self):
        for p in reversed(self.patchers):
            p.stop()
        self.tmp.cleanup()

    @staticmethod
    def request_for(data: bytes) -> Request:
        sent = False
        async def receive():
            nonlocal sent
            if sent:
                return {"type": "http.disconnect"}
            sent = True
            return {"type": "http.request", "body": data, "more_body": False}
        scope = {"type": "http", "asgi": {"version": "3.0"}, "http_version": "1.1", "method": "PUT",
                 "scheme": "https", "path": "/api/uploads/test/chunks/0", "raw_path": b"/api/uploads/test/chunks/0",
                 "query_string": b"", "headers": [], "client": ("127.0.0.1", 54321), "server": ("127.0.0.1", 8765)}
        return Request(scope, receive)

    async def test_resume_missing_chunks_pause_and_sha256(self):
        payload = bytes(range(251)) * 1600
        started = uploads.start_upload("alice", "folder/archive.bin", len(payload), 12345, "192.168.1.22")
        first = payload[:started["chunk_size"]]
        await uploads.save_chunk(started["upload_id"], 0, "alice", self.request_for(first))
        uploads.pause_upload(started["upload_id"], "alice", True)
        resumed = uploads.start_upload("alice", "folder/archive.bin", len(payload), 12345, "192.168.1.22")
        self.assertTrue(resumed["resumed"])
        self.assertEqual(resumed["upload_id"], started["upload_id"])
        self.assertEqual(resumed["received_chunks"], [0])
        for index in range(1, (len(payload) + resumed["chunk_size"] - 1) // resumed["chunk_size"]):
            lo = index * resumed["chunk_size"]
            chunk = payload[lo:lo + resumed["chunk_size"]]
            await uploads.save_chunk(started["upload_id"], index, "alice", self.request_for(chunk))
        result = await uploads.complete_upload(started["upload_id"], "alice")
        saved = self.base / "folder" / "archive.bin"
        self.assertEqual(saved.read_bytes(), payload)
        self.assertEqual(result["sha256"], hashlib.sha256(payload).hexdigest())

    async def test_complete_refuses_missing_chunks(self):
        started = uploads.start_upload("alice", "partial.txt", 9, 99, "127.0.0.1")
        with self.assertRaises(HTTPException) as error:
            await uploads.complete_upload(started["upload_id"], "alice")
        self.assertEqual(error.exception.status_code, 409)


if __name__ == "__main__":
    unittest.main()
