package emit

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/eventid"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

type memArchive map[string][]byte

func (m memArchive) Put(_ context.Context, k string, b []byte) error {
	if strings.Contains(k, "gagal") {
		return errors.New("disk penuh")
	}
	m[k] = b
	return nil
}

type ev struct{ key, val string }

func (e ev) Subject() string { return "raw.uji.test" }
func (e ev) Key() string     { return e.key }
func (e ev) Content() ([]byte, error) {
	if e.val == "" {
		return nil, errors.New("kosong")
	}
	return []byte(e.val), nil
}

func (e ev) Encode(m ports.FetchMeta) ([]byte, error) {
	if e.val == "x" {
		return nil, errors.New("encode gagal")
	}
	return []byte(e.val + "|" + m.ArchiveKey), nil
}

type pub struct {
	msgs []ports.Message
	err  error
}

func (p *pub) Publish(_ context.Context, m ports.Message) (ports.PublishResult, error) {
	p.msgs = append(p.msgs, m)
	return ports.PublishResult{Duplicate: len(p.msgs) > 1}, p.err
}

func TestArchive(t *testing.T) {
	at := time.Date(2026, 9, 24, 7, 20, 5, 0, time.FixedZone("WIB", 7*3600))
	body := []byte("isi payload")
	sum := Sum(body)
	if len(sum) != 64 {
		t.Fatal(sum)
	}
	if k := ArchiveKey("bmkg-cap", "xml", sum, at); k != "bmkg-cap/2026/09/24/002005Z-"+sum[:12]+".xml.gz" {
		t.Fatal(k)
	}
	if k := ArchiveKey("c", "x", "ab", at); !strings.HasSuffix(k, "Z-ab.x.gz") {
		t.Fatal(k)
	}
	a := memArchive{}
	key, err := Archive(t.Context(), a, "bmkg-cap", "xml", sum, body, at)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(a[key]))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(zr)
	if !bytes.Equal(got, body) {
		t.Fatal("isi arsip berbeda")
	}
	if _, err := Archive(t.Context(), a, "gagal", "xml", sum, body, at); err == nil {
		t.Fatal("galat arsip harus dikembalikan")
	}
}

func TestContentAndPublish(t *testing.T) {
	e := ev{key: "k", val: "v"}
	content, sum, err := Content(e)
	if err != nil || string(content) != "v" || sum != Sum([]byte("v")) {
		t.Fatal(content, sum, err)
	}
	if _, _, err := Content(ev{key: "k"}); err == nil {
		t.Fatal("galat isi harus dikembalikan")
	}
	p := &pub{}
	meta := ports.FetchMeta{Connector: "uji", ArchiveKey: "a"}
	if _, err := Publish(t.Context(), p, e, content, meta); err != nil {
		t.Fatal(err)
	}
	m := p.msgs[0]
	if m.Subject != "raw.uji.test" || m.ID != eventid.MsgID("uji", "k", content) || string(m.Data) != "v|a" {
		t.Fatalf("%+v", m)
	}
	if res, _ := Publish(t.Context(), p, e, content, meta); !res.Duplicate {
		t.Fatal("hasil broker tidak diteruskan")
	}
	if _, err := Publish(t.Context(), p, ev{key: "k", val: "x"}, content, meta); err == nil {
		t.Fatal("galat encode harus dikembalikan")
	}
	p.err = errors.New("NATS mati")
	if _, err := Publish(t.Context(), p, e, content, meta); err == nil || !strings.Contains(err.Error(), "raw.uji.test") {
		t.Fatalf("err = %v", err)
	}
}
