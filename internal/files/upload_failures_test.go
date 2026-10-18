package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

type failingChunkReader struct {
	afterRead func()
}

func (r failingChunkReader) Read(p []byte) (int, error) {
	n := copy(p, "unconfirmed")
	if r.afterRead != nil {
		r.afterRead()
		return n, nil
	}
	return n, io.ErrUnexpectedEOF
}

func TestUploadRecoversFromChunkFailures(t *testing.T) {
	for _, failure := range []string{"reader", "cancelled", "database", "disk-reserve"} {
		t.Run(failure, func(t *testing.T) {
			s, a := testService(t)
			ctx := context.Background()
			data := []byte("saved prefix followed by confirmed contents")
			const offset = 12
			u, err := s.Begin(ctx, a, "recover.bin", int64(len(data)), sum(data), "skip")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Chunk(ctx, a, u.ID, 0, offset, bytes.NewReader(data[:offset])); err != nil {
				t.Fatal(err)
			}
			chunkCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			var reader io.Reader = bytes.NewReader(bytes.Repeat([]byte("x"), len(data)-offset))
			switch failure {
			case "reader":
				reader = failingChunkReader{}
			case "cancelled":
				reader = failingChunkReader{afterRead: cancel}
			case "database":
				if _, err = s.DB.Exec(`CREATE TRIGGER refuse_chunk BEFORE UPDATE OF offset ON uploads BEGIN SELECT RAISE(ABORT,'offset unavailable'); END`); err != nil {
					t.Fatal(err)
				}
			case "disk-reserve":
				if _, err = s.DB.Exec("UPDATE file_settings SET critical_free=9000000000000000000"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.Chunk(chunkCtx, a, u.ID, offset, int64(len(data)-offset), reader); err == nil {
				t.Fatal("accepted failed chunk")
			}
			if failure == "disk-reserve" && !errors.Is(err, ErrQuota) {
				t.Fatal(err)
			}
			if failure == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			state, err := s.Upload(ctx, a, u.ID)
			if err != nil || state.Offset != offset || state.State != "pending" {
				t.Fatal(state, err)
			}
			part, err := s.Root.ReadFile(".lanbridge/uploads/" + u.ID + ".part")
			if err != nil || len(part) < offset || !bytes.Equal(part[:offset], data[:offset]) {
				t.Fatal("confirmed prefix lost", err)
			}
			if failure != "database" && len(part) != offset {
				t.Fatal("failed read retained unconfirmed bytes", len(part))
			}
			// A failed offset update may leave a disk tail. The next PATCH must
			// discard it using the committed database offset before appending.
			if _, err = s.DB.Exec("DROP TRIGGER IF EXISTS refuse_chunk"); err != nil {
				t.Fatal(err)
			}
			if _, err = s.DB.Exec("UPDATE file_settings SET critical_free=67108864"); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Chunk(ctx, a, u.ID, offset, int64(len(data)-offset), bytes.NewReader(data[offset:])); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Finish(ctx, a, u.ID); err != nil {
				t.Fatal(err)
			}
			actual, err := s.Root.ReadFile("recover.bin")
			if err != nil || !bytes.Equal(actual, data) {
				t.Fatal("resumed content mismatch", err)
			}
		})
	}
}
