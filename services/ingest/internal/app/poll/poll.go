// Package poll adalah use case satu kali polling satu konektor: ambil payload,
// arsipkan bila berubah, parse, lalu terbitkan record baru atau yang direvisi.
package poll

import (
	"context"
	"errors"
	"fmt"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/emit"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// ErrParse membungkus galat saat payload tidak bisa dibaca sama sekali.
var ErrParse = errors.New("payload sumber tidak bisa dibaca")

// MaxRejectionsKept membatasi contoh penolakan dan kegagalan di Result.
const MaxRejectionsKept = 10

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
	// Contoh penolakan, paling banyak MaxRejectionsKept.
	Rejections []ports.Rejection
	// Failed adalah record yang gagal diambil (misal dokumen detail CAP) dan
	// akan dicoba lagi di polling berikutnya; polling itu sendiri tetap sukses.
	Failed int
	// Contoh kegagalan, paling banyak MaxRejectionsKept.
	Failures   []ports.Rejection
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
		return res, fmt.Errorf("%s: mengambil %s: %w", p.conn.Name(), req.Redacted(), err)
	}
	fetchedAt := p.clock.Now().UTC()
	if resp.NotModified {
		res.NotModified = true
		return res, nil
	}
	sum := emit.Sum(resp.Body)
	if sum == p.doneSum {
		res.Unchanged = true
		p.etag, p.lastModified = resp.ETag, resp.LastModified
		return res, nil
	}

	if p.archive != nil {
		if sum == p.archivedSum {
			res.ArchiveKey = p.archivedKey
		} else if key, err := emit.Archive(ctx, p.archive, p.conn.Name(), p.conn.ArchiveExt(), sum, resp.Body, fetchedAt); err != nil {
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
	res.Rejections = rejections[:min(len(rejections), MaxRejectionsKept)]

	meta := ports.FetchMeta{
		Connector: p.conn.Name(), FetchedAt: fetchedAt,
		ArchiveKey: res.ArchiveKey, PayloadSHA256: sum,
	}
	current := make(map[string]string, len(events))
	for _, ev := range events {
		content, contentSum, err := emit.Content(ev)
		if err != nil {
			return res, fmt.Errorf("%s: %w", p.conn.Name(), err)
		}
		current[ev.Key()] = contentSum
		if p.seen[ev.Key()] == contentSum {
			res.AlreadySeen++
			continue
		}
		ack, err := emit.Publish(ctx, p.pub, ev, content, meta)
		if err != nil {
			return res, fmt.Errorf("%s: %w", p.conn.Name(), err)
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
