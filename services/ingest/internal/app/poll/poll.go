// Package poll adalah use case satu kali polling satu konektor: ambil payload,
// arsipkan bila berubah, parse, lalu terbitkan record baru atau yang direvisi.
package poll

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/eventid"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// ErrParse membungkus galat saat payload tidak bisa dibaca sama sekali.
var ErrParse = errors.New("payload sumber tidak bisa dibaca")

// maxRejectionsKept membatasi contoh penolakan yang disimpan di Result.
const maxRejectionsKept = 10

// Poller menyimpan status satu konektor antar-polling. Tidak aman dipakai
// bersamaan; runner menjalankan satu goroutine per konektor.
type Poller struct {
	conn    ports.Connector
	fetch   ports.Fetcher
	archive ports.Archive // nil berarti arsip dimatikan
	pub     ports.Publisher
	clock   ports.Clock

	etag, lastModified string
	// Hash payload terakhir yang semua record-nya sudah terbit.
	doneSum string
	// Hash payload terakhir yang sudah masuk arsip, supaya retry tidak
	// mengarsipkan payload yang sama dua kali.
	archivedSum, archivedKey string
	// seen memetakan kunci record ke hash isi yang sudah terbit.
	seen map[string]string
}

// New membuat Poller. archive boleh nil.
func New(conn ports.Connector, fetch ports.Fetcher, archive ports.Archive, pub ports.Publisher, clock ports.Clock) *Poller {
	return &Poller{conn: conn, fetch: fetch, archive: archive, pub: pub, clock: clock, seen: map[string]string{}}
}

// Name mengembalikan nama konektor.
func (p *Poller) Name() string { return p.conn.Name() }

// Result merangkum satu polling.
type Result struct {
	NotModified bool // sumber menjawab 304
	Unchanged   bool // payload identik dengan yang terakhir diproses
	Events      int  // record valid di payload
	Published   int  // pesan baru yang diterima broker
	Duplicates  int  // pesan yang ditolak broker karena ID sudah ada
	AlreadySeen int  // record yang isinya sudah pernah terbit
	Rejected    int
	// Contoh penolakan, paling banyak maxRejectionsKept.
	Rejections []ports.Rejection
	ArchiveKey string
	// ArchiveErr tidak menggagalkan polling: peringatan lebih penting daripada arsip.
	ArchiveErr error
}

// Poll menjalankan satu polling.
func (p *Poller) Poll(ctx context.Context) (Result, error) {
	var res Result
	req := p.conn.Request()
	req.ETag, req.LastModified = p.etag, p.lastModified

	resp, err := p.fetch.Fetch(ctx, req)
	if err != nil {
		return res, fmt.Errorf("%s: mengambil %s: %w", p.conn.Name(), req.URL, err)
	}
	fetchedAt := p.clock.Now().UTC()
	if resp.NotModified {
		res.NotModified = true
		return res, nil
	}
	digest := sha256.Sum256(resp.Body)
	sum := hex.EncodeToString(digest[:])
	if sum == p.doneSum {
		res.Unchanged = true
		p.etag, p.lastModified = resp.ETag, resp.LastModified
		return res, nil
	}

	if p.archive != nil {
		if sum == p.archivedSum {
			res.ArchiveKey = p.archivedKey
		} else if key, err := p.store(ctx, sum, resp.Body, fetchedAt); err != nil {
			res.ArchiveErr = err
		} else {
			res.ArchiveKey = key
			p.archivedSum, p.archivedKey = sum, key
		}
	}

	events, rejections, err := p.conn.Parse(resp.Body, fetchedAt)
	if err != nil {
		return res, fmt.Errorf("%s: %w: %w", p.conn.Name(), ErrParse, err)
	}
	res.Events, res.Rejected = len(events), len(rejections)
	res.Rejections = rejections[:min(len(rejections), maxRejectionsKept)]

	meta := ports.FetchMeta{
		Connector: p.conn.Name(), FetchedAt: fetchedAt,
		ArchiveKey: res.ArchiveKey, PayloadSHA256: sum,
	}
	current := make(map[string]string, len(events))
	for _, ev := range events {
		content, err := ev.Content()
		if err != nil {
			return res, fmt.Errorf("%s: serialisasi record %s: %w", p.conn.Name(), ev.Key(), err)
		}
		contentDigest := sha256.Sum256(content)
		contentSum := hex.EncodeToString(contentDigest[:])
		current[ev.Key()] = contentSum
		if p.seen[ev.Key()] == contentSum {
			res.AlreadySeen++
			continue
		}
		data, err := ev.Encode(meta)
		if err != nil {
			return res, fmt.Errorf("%s: encode record %s: %w", p.conn.Name(), ev.Key(), err)
		}
		msg := ports.Message{Subject: ev.Subject(), ID: eventid.MsgID(p.conn.Name(), ev.Key(), content), Data: data}
		ack, err := p.pub.Publish(ctx, msg)
		if err != nil {
			return res, fmt.Errorf("%s: menerbitkan %s ke %s: %w", p.conn.Name(), ev.Key(), msg.Subject, err)
		}
		if ack.Duplicate {
			res.Duplicates++
		} else {
			res.Published++
		}
		p.seen[ev.Key()] = contentSum
	}
	// Record yang sudah keluar dari feed dilupakan supaya memori tidak tumbuh;
	// bila muncul lagi, JetStream menolak ID gandanya selama jendela duplikat.
	p.seen = current
	p.doneSum = sum
	// Validator baru dipakai hanya setelah semua record terbit; kalau disimpan
	// lebih awal, sumber akan menjawab 304 dan record yang gagal tidak pernah dicoba lagi.
	p.etag, p.lastModified = resp.ETag, resp.LastModified
	return res, nil
}

// store mengompres payload lalu menyimpannya di arsip dengan kunci
// <konektor>/<YYYY>/<MM>/<DD>/<hhmmss>Z-<sha256[:12]>.<ext>.gz (UTC).
func (p *Poller) store(ctx context.Context, sum string, body []byte, fetchedAt time.Time) (string, error) {
	key := fmt.Sprintf("%s/%s-%s.%s.gz", p.conn.Name(), fetchedAt.Format("2006/01/02/150405Z"), sum[:12], p.conn.ArchiveExt())
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(body); err != nil {
		return "", fmt.Errorf("kompresi arsip: %w", err)
	}
	if err := zw.Close(); err != nil {
		return "", fmt.Errorf("kompresi arsip: %w", err)
	}
	if err := p.archive.Put(ctx, key, buf.Bytes()); err != nil {
		return "", fmt.Errorf("arsip %s: %w", key, err)
	}
	return key, nil
}
