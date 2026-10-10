package files

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"lanbridge/internal/store"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testService(t *testing.T) (*Service, Actor) {
	t.Helper()
	db, e := store.Open(filepath.Join(t.TempDir(), "files.sqlite3"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO users(id,username,password_hash,role,created_at) VALUES(1,'test','unused','admin',0),(2,'user','unused','user',0)"); e != nil {
		t.Fatal(e)
	}
	s, e := New(t.TempDir(), db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close(); db.Close() })
	return s, Actor{1, true}
}

func TestMoveRecoveryKeepsInheritedPermissions(t *testing.T) {
	s, a := testService(t)
	ctx := context.Background()
	if e := s.Mkdir(ctx, a, "private/child"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec("INSERT INTO file_acl VALUES('private','admin')"); e != nil {
		t.Fatal(e)
	}
	o, e := s.planOperation(ctx, "private/child", "moved", "move", a.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Root.Rename(o.Source, o.Target); e != nil {
		t.Fatal(e)
	}
	ordinary := Actor{ID: 2}
	if e = s.Access(ctx, ordinary, "moved", false); !errors.Is(e, ErrAccess) {
		t.Fatal("pending move exposed", e)
	}
	if e = s.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.Access(ctx, ordinary, "moved", false); !errors.Is(e, ErrAccess) {
		t.Fatal("inherited ACL lost", e)
	}
	if _, e = s.Copy(ctx, a, "moved", "copied", "skip"); e != nil {
		t.Fatal(e)
	}
	if e = s.Access(ctx, ordinary, "copied", false); !errors.Is(e, ErrAccess) {
		t.Fatal("copy ACL lost", e)
	}
}
func TestACLFilteringBeforePagination(t *testing.T) {
	s, a := testService(t)
	ctx := context.Background()
	for _, name := range []string{"a-hidden", "b-hidden", "c-visible"} {
		if e := s.Mkdir(ctx, a, name); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.DB.Exec("INSERT INTO file_acl VALUES('a-hidden','admin'),('b-hidden','admin')"); e != nil {
		t.Fatal(e)
	}
	entries, e := s.List(ctx, Actor{ID: 2}, ".", "", "name", "asc", 0, 1)
	if e != nil || len(entries) != 1 || entries[0].Name != "c-visible" {
		t.Fatal(entries, e)
	}
}
func TestCaseAliasesCannotBypassACL(t *testing.T) {
	s, a := testService(t)
	ctx := context.Background()
	if e := s.Mkdir(ctx, a, "Secret"); e != nil {
		t.Fatal(e)
	}
	if e := s.Root.WriteFile("Secret/data.txt", []byte("secret"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := s.SetAccess(ctx, a, "Secret", "admin"); e != nil {
		t.Fatal(e)
	}
	if f, e := s.Open(ctx, Actor{ID: 2}, "secret/data.txt"); e == nil {
		f.Close()
		t.Fatal("case alias bypassed ACL")
	}
}
func TestTrashRestoreRecoveryAndExpiration(t *testing.T) {
	s, a := testService(t)
	ctx := context.Background()
	if e := s.Mkdir(ctx, a, "private"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec("INSERT INTO file_acl VALUES('private','admin')"); e != nil {
		t.Fatal(e)
	}
	id, e := s.Trash(ctx, a, "private")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec("UPDATE trash SET path='restored',state='restoring' WHERE id=?", id); e != nil {
		t.Fatal(e)
	}
	if e = s.Root.Rename(".lanbridge/trash/"+id, "restored"); e != nil {
		t.Fatal(e)
	}
	if e = s.Access(ctx, Actor{ID: 2}, "restored", false); !errors.Is(e, ErrAccess) {
		t.Fatal("restore exposed", e)
	}
	if e = s.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.Access(ctx, Actor{ID: 2}, "restored", false); !errors.Is(e, ErrAccess) {
		t.Fatal("restored ACL lost", e)
	}
	id, e = s.Trash(ctx, a, "restored")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec("UPDATE trash SET created=? WHERE id=?", time.Now().Add(-31*24*time.Hour).Unix(), id); e != nil {
		t.Fatal(e)
	}
	u, e := s.Begin(ctx, a, "expired.txt", 1, sum([]byte("x")), "skip")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec("UPDATE uploads SET updated=0 WHERE id=?", u.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Maintenance(ctx); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{".lanbridge/trash/" + id, ".lanbridge/uploads/" + u.ID + ".part"} {
		if _, e = s.Root.Lstat(name); !errors.Is(e, os.ErrNotExist) {
			t.Fatal(name, e)
		}
	}
}
func sum(data []byte) string { hash := sha256.Sum256(data); return hex.EncodeToString(hash[:]) }
func TestPathValidation(t *testing.T) {
	for _, value := range []string{"../escape", "/etc/passwd", `C:\Windows`, "a/../b", "a//b", "CON.txt", "dir/NUL", "com1", "LPT².txt", "bad.", "bad ", "a\x00b", ".lanbridge/x", "x/.LANBRIDGE/y"} {
		if _, e := Clean(value); e == nil {
			t.Errorf("accepted %q", value)
		}
	}
	for _, value := range []string{"Фото/Лето 2026.png", "a/b/c", "COM10.txt"} {
		if _, e := Clean(value); e != nil {
			t.Fatal(value, e)
		}
	}
	if result, e := Clean("e\u0301.txt"); e != nil || result != "é.txt" {
		t.Fatal(result, e)
	}
}
func TestIndexAndInheritedACL(t *testing.T) {
	s, a := testService(t)
	ctx := context.Background()
	if e := s.Mkdir(ctx, a, "Фото/Пустая"); e != nil {
		t.Fatal(e)
	}
	if e := s.Root.WriteFile("Фото/ФАЙЛ.txt", []byte("data"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := s.Index(ctx); e != nil {
		t.Fatal(e)
	}
	entries, e := s.List(ctx, a, ".", "файл", "name", "asc", 0, 100)
	if e != nil || len(entries) != 1 {
		t.Fatal(entries, e)
	}
	if _, e = s.DB.Exec("INSERT INTO file_acl VALUES('Фото','admin')"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.List(ctx, Actor{2, false}, "Фото", "", "name", "asc", 0, 100); !errors.Is(e, ErrAccess) {
		t.Fatal(e)
	}
	if _, e = s.Open(ctx, a, "../escape"); e == nil {
		t.Fatal("escaped root")
	}
}
func TestResumableUploadAndChecksum(t *testing.T) {
	s, a := testService(t)
	ctx := context.Background()
	data := []byte("resumable contents")
	u, e := s.Begin(ctx, a, "folder/file.txt", int64(len(data)), sum(data), "skip")
	if e != nil {
		t.Fatal(e)
	}
	u, e = s.Chunk(ctx, a, u.ID, 0, 5, bytes.NewReader(data[:5]))
	if e != nil || u.Offset != 5 {
		t.Fatal(u, e)
	}
	// A process can stop after disk write but before the SQLite offset update.
	f, e := s.Root.OpenFile(".lanbridge/uploads/"+u.ID+".part", os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	f.Write([]byte("unconfirmed bytes"))
	f.Close()
	if _, e = s.Chunk(ctx, a, u.ID, 0, 1, bytes.NewReader(data)); e == nil {
		t.Fatal("accepted stale offset")
	}
	u, e = s.Chunk(ctx, a, u.ID, 5, int64(len(data)-5), bytes.NewReader(data[5:]))
	if e != nil {
		t.Fatal(e)
	}
	u, e = s.Finish(ctx, a, u.ID)
	if e != nil || u.State != "complete" {
		t.Fatal(u, e)
	}
	actual, e := s.Root.ReadFile("folder/file.txt")
	if e != nil || !bytes.Equal(actual, data) {
		t.Fatal(string(actual), e)
	}
	if _, e = s.Begin(ctx, a, "folder/FILE.txt", 0, sum(nil), "skip"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	duplicate, e := s.Begin(ctx, a, "folder/file.txt", 0, sum(nil), "both")
	if e != nil || duplicate.Path != "folder/file (1).txt" {
		t.Fatal(duplicate, e)
	}
	if _, e = s.Finish(ctx, a, duplicate.ID); e != nil {
		t.Fatal(e)
	}
	bad, e := s.Begin(ctx, a, "bad.txt", 4, sum([]byte("good")), "skip")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Chunk(ctx, a, bad.ID, 0, 4, bytes.NewReader([]byte("bad!"))); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Finish(ctx, a, bad.ID); e == nil {
		t.Fatal("accepted invalid digest")
	}
	if _, e = s.Root.Stat("bad.txt"); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
}
func TestQuotaAndUploadOwnership(t *testing.T) {
	s, a := testService(t)
	ctx := context.Background()
	if _, e := s.DB.Exec("UPDATE file_settings SET global_limit=3"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Begin(ctx, a, "large", 4, sum([]byte("data")), "skip"); !errors.Is(e, ErrQuota) {
		t.Fatal(e)
	}
	u, e := s.Begin(ctx, a, "small", 3, sum([]byte("abc")), "skip")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Upload(ctx, Actor{2, false}, u.ID); !errors.Is(e, ErrAccess) {
		t.Fatal(e)
	}
	if _, e = s.Chunk(ctx, a, u.ID, 0, 3, bytes.NewReader([]byte("a"))); e == nil {
		t.Fatal("accepted short chunk")
	}
	loaded, e := s.Upload(ctx, a, u.ID)
	if e != nil || loaded.Offset != 0 {
		t.Fatal(loaded, e)
	}
}
func TestRootRejectsEscapingSymlink(t *testing.T) {
	s, a := testService(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if e := os.WriteFile(outside, []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(s.Root.Name(), "escape")); e != nil {
		t.Skip("symlink creation unavailable on this host")
	}
	if _, e := s.Open(context.Background(), a, "escape"); e == nil {
		t.Fatal("read escaping symlink")
	}
}

func TestDirectoriesCopyMoveTrashAndStreamingZIP(t *testing.T) {
	s, a := testService(t)
	ctx := context.Background()
	if e := s.Mkdir(ctx, a, "original/empty"); e != nil {
		t.Fatal(e)
	}
	if e := s.Root.WriteFile("original/data.txt", []byte("content"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := s.Index(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Copy(ctx, a, "original", "copy", "skip"); e != nil {
		t.Fatal(e)
	}
	if info, e := s.Root.Stat("copy/empty"); e != nil || !info.IsDir() {
		t.Fatal(info, e)
	}
	if _, e := s.Move(ctx, a, "copy", "renamed", "skip"); e != nil {
		t.Fatal(e)
	}
	var output bytes.Buffer
	if e := s.Zip(ctx, a, []string{"renamed", "renamed/data.txt"}, &output); e != nil {
		t.Fatal(e)
	}
	reader, e := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if e != nil {
		t.Fatal(e)
	}
	if len(reader.File) != 3 {
		t.Fatal("empty directory lost or duplicate entries", len(reader.File))
	}
	id, e := s.Trash(ctx, a, "renamed")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Root.Stat("renamed"); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
	if _, e = s.Restore(ctx, a, id, "skip"); e != nil {
		t.Fatal(e)
	}
	if data, e := s.Root.ReadFile("renamed/data.txt"); e != nil || string(data) != "content" {
		t.Fatal(string(data), e)
	}
}

func TestRecoverPublishedHardLink(t *testing.T) {
	s, a := testService(t)
	ctx := context.Background()
	data := []byte("durable")
	u, e := s.Begin(ctx, a, "recovered.txt", int64(len(data)), sum(data), "skip")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Chunk(ctx, a, u.ID, 0, int64(len(data)), bytes.NewReader(data)); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec("UPDATE uploads SET state='committing' WHERE id=?", u.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Root.Link(".lanbridge/uploads/"+u.ID+".part", u.Path); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Finish(ctx, a, u.ID); e != nil {
		t.Fatal(e)
	}
}
