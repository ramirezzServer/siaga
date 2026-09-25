package archivetool

import (
	"context"
	"errors"
	"iter"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/emit"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

type memArchive struct {
	objs            map[string][]byte
	listErr, getErr error
	putErr          error
	puts            int
}

func newMem() *memArchive { return &memArchive{objs: map[string][]byte{}} }

func (m *memArchive) Put(_ context.Context, k string, b []byte) error {
	if m.putErr != nil {
		return m.putErr
	}
	m.puts++
	m.objs[k] = b
	return nil
}

func (m *memArchive) Get(_ context.Context, k string) ([]byte, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.objs[k], nil
}

func (m *memArchive) List(_ context.Context, prefix, after string) iter.Seq2[ports.ArchiveObject, error] {
	return func(yield func(ports.ArchiveObject, error) bool) {
		if m.listErr != nil {
			yield(ports.ArchiveObject{}, m.listErr)
			return
		}
		var keys []string
		for k := range m.objs {
			if strings.HasPrefix(k, prefix) && k > after {
				keys = append(keys, k)
			}
		}
		slices.Sort(keys)
		for _, k := range keys {
			if !yield(ports.ArchiveObject{Key: k, Size: int64(len(m.objs[k]))}, nil) {
				return
			}
		}
	}
}

var t0 = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func fill(t *testing.T) *memArchive {
	t.Helper()
	m := newMem()
	for i, spec := range []struct {
		conn string
		at   time.Duration
	}{{"bmkg", 0}, {"bmkg", time.Hour}, {"usgs", 30 * time.Minute}, {"bmkg", -time.Hour}} {
		body := []byte(strings.Repeat("x", i+1))
		if _, err := emit.Archive(t.Context(), m, spec.conn, "json", emit.Sum(body), body, t0.Add(spec.at)); err != nil {
			t.Fatal(err)
		}
	}
	m.objs["bmkg/catatan.txt"] = []byte("asing")
	return m
}

func TestSummarize(t *testing.T) {
	m := fill(t)
	s, err := Summarize(t.Context(), m, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Foreign != 1 || len(s.Connectors) != 2 {
		t.Fatalf("%+v", s)
	}
	b := s.Connectors[0]
	if b.Connector != "bmkg" || b.Objects != 3 || !b.First.Equal(t0.Add(-time.Hour)) || !b.Last.Equal(t0.Add(time.Hour)) || b.Bytes <= 0 {
		t.Fatalf("%+v", b)
	}
	m.listErr = errors.New("jaringan")
	if _, err := Summarize(t.Context(), m, ""); err == nil {
		t.Fatal("galat daftar harus diteruskan")
	}
}

func TestVerify(t *testing.T) {
	m := fill(t)
	for k := range m.objs {
		if strings.HasPrefix(k, "usgs/") {
			m.objs[k] = []byte("rusak")
		}
	}
	rep, err := Verify(t.Context(), m, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Objects != 5 || rep.OK != 3 || rep.Corrupt != 2 || len(rep.Samples) != 2 {
		t.Fatalf("%+v", rep)
	}
	m.getErr = errors.New("disk")
	if _, err := Verify(t.Context(), m, ""); err == nil {
		t.Fatal("galat baca harus diteruskan")
	}
	m.getErr, m.listErr = nil, errors.New("jaringan")
	if _, err := Verify(t.Context(), m, ""); err == nil {
		t.Fatal("galat daftar harus diteruskan")
	}
}

func TestCopy(t *testing.T) {
	src, dst := fill(t), newMem()
	var progress int
	rep, err := Copy(t.Context(), src, dst, "bmkg/", func(CopyReport) { progress++ })
	if err != nil {
		t.Fatal(err)
	}
	if rep.Copied != 3 || rep.Skipped != 1 || rep.Existing != 0 || progress != 3 || rep.Bytes <= 0 || len(dst.objs) != 3 {
		t.Fatalf("%+v", rep)
	}
	// Idempotent: salinan kedua tidak menulis apa pun.
	rep, err = Copy(t.Context(), src, dst, "", nil)
	if err != nil || rep.Copied != 1 || rep.Existing != 3 || dst.puts != 4 {
		t.Fatalf("%+v, %v", rep, err)
	}
	// Objek setengah jadi di tujuan (ukuran beda) ditulis ulang.
	for k := range dst.objs {
		dst.objs[k] = dst.objs[k][:1]
		break
	}
	if rep, err := Copy(t.Context(), src, dst, "", nil); err != nil || rep.Copied != 1 {
		t.Fatalf("%+v, %v", rep, err)
	}
	for name, setup := range map[string]func(s, d *memArchive){
		"daftar tujuan": func(_, d *memArchive) { d.listErr = errors.New("x") },
		"daftar sumber": func(s, _ *memArchive) { s.listErr = errors.New("x") },
		"baca sumber":   func(s, _ *memArchive) { s.getErr = errors.New("x") },
		"tulis tujuan":  func(_, d *memArchive) { d.putErr = errors.New("x") },
	} {
		s, d := fill(t), newMem()
		setup(s, d)
		if _, err := Copy(t.Context(), s, d, "", nil); err == nil {
			t.Errorf("%s: galat harus diteruskan", name)
		}
	}
}
