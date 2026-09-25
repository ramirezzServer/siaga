// Package archivetool adalah use case perawatan arsip payload mentah:
// ringkasan isi per konektor, verifikasi integritas, dan penyalinan antar-
// penyimpanan (misal folder lokal fase 1a–1d ke Garage).
package archivetool

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/emit"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/archivekey"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// MaxSamples membatasi contoh galat di laporan.
const MaxSamples = 20

// ConnectorSummary adalah isi arsip satu konektor.
type ConnectorSummary struct {
	Connector string    `json:"connector"`
	Objects   int       `json:"objects"`
	Bytes     int64     `json:"bytes"`
	First     time.Time `json:"first"`
	Last      time.Time `json:"last"`
}

// Summary adalah ringkasan arsip.
type Summary struct {
	Connectors []ConnectorSummary `json:"connectors"`
	// Foreign adalah objek yang kuncinya bukan format baku.
	Foreign int `json:"foreign"`
}

// Summarize meringkas objek berawalan prefix per konektor, urut nama.
func Summarize(ctx context.Context, src ports.ArchiveReader, prefix string) (Summary, error) {
	var sum Summary
	by := map[string]*ConnectorSummary{}
	for o, err := range src.List(ctx, prefix, "") {
		if err != nil {
			return sum, err
		}
		k, err := archivekey.Parse(o.Key)
		if err != nil {
			sum.Foreign++
			continue
		}
		c := by[k.Connector]
		if c == nil {
			c = &ConnectorSummary{Connector: k.Connector, First: k.FetchedAt, Last: k.FetchedAt}
			by[k.Connector] = c
		}
		c.Objects++
		c.Bytes += o.Size
		if k.FetchedAt.Before(c.First) {
			c.First = k.FetchedAt
		}
		if k.FetchedAt.After(c.Last) {
			c.Last = k.FetchedAt
		}
	}
	for _, c := range by {
		sum.Connectors = append(sum.Connectors, *c)
	}
	slices.SortFunc(sum.Connectors, func(a, b ConnectorSummary) int { return cmp.Compare(a.Connector, b.Connector) })
	return sum, nil
}

// VerifyReport adalah hasil Verify.
type VerifyReport struct {
	Objects int      `json:"objects"`
	OK      int      `json:"ok"`
	Corrupt int      `json:"corrupt"`
	Samples []string `json:"samples,omitempty"`
}

// Verify membaca setiap objek berawalan prefix dan memastikan kuncinya baku,
// gzip-nya utuh, dan SHA-256 isinya cocok dengan kunci. Objek rusak dicatat;
// galat baca arsip menghentikan verifikasi.
func Verify(ctx context.Context, src ports.ArchiveReader, prefix string) (VerifyReport, error) {
	var rep VerifyReport
	for o, err := range src.List(ctx, prefix, "") {
		if err != nil {
			return rep, err
		}
		rep.Objects++
		data, err := src.Get(ctx, o.Key)
		if err != nil {
			return rep, fmt.Errorf("membaca %s: %w", o.Key, err)
		}
		if _, _, _, err := emit.Unarchive(o.Key, data, emit.DefaultMaxPayload); err != nil {
			rep.Corrupt++
			if len(rep.Samples) < MaxSamples {
				rep.Samples = append(rep.Samples, err.Error())
			}
			continue
		}
		rep.OK++
	}
	return rep, nil
}

// CopyReport adalah hasil Copy.
type CopyReport struct {
	// Copied objek yang ditulis ke tujuan; Existing sudah ada di tujuan.
	Copied   int   `json:"copied"`
	Existing int   `json:"existing"`
	Bytes    int64 `json:"bytes"`
	// Skipped: objek rusak di sumber yang sengaja tidak disalin.
	Skipped int      `json:"skipped"`
	Samples []string `json:"samples,omitempty"`
}

// Copy menyalin objek berawalan prefix dari src ke dst yang belum ada di dst.
// Setiap objek diverifikasi dulu (emit.Unarchive); objek rusak tidak disalin.
// Idempotent: menjalankan ulang setelah terputus hanya menyalin sisanya.
// progress (boleh nil) dipanggil setelah setiap objek.
func Copy(ctx context.Context, src ports.ArchiveReader, dst ports.ArchiveStore, prefix string, progress func(CopyReport)) (CopyReport, error) {
	var rep CopyReport
	existing := map[string]int64{}
	for o, err := range dst.List(ctx, prefix, "") {
		if err != nil {
			return rep, fmt.Errorf("daftar tujuan: %w", err)
		}
		existing[o.Key] = o.Size
	}
	for o, err := range src.List(ctx, prefix, "") {
		if err != nil {
			return rep, fmt.Errorf("daftar sumber: %w", err)
		}
		// Ukuran sama berarti objek sudah utuh di tujuan (Put tidak pernah
		// meninggalkan objek setengah jadi); ukuran beda ditulis ulang.
		if size, ok := existing[o.Key]; ok && size == o.Size {
			rep.Existing++
			continue
		}
		data, err := src.Get(ctx, o.Key)
		if err != nil {
			return rep, fmt.Errorf("membaca %s: %w", o.Key, err)
		}
		if _, _, _, err := emit.Unarchive(o.Key, data, emit.DefaultMaxPayload); err != nil {
			rep.Skipped++
			if len(rep.Samples) < MaxSamples {
				rep.Samples = append(rep.Samples, err.Error())
			}
			continue
		}
		if err := dst.Put(ctx, o.Key, data); err != nil {
			return rep, fmt.Errorf("menulis %s: %w", o.Key, err)
		}
		rep.Copied++
		rep.Bytes += int64(len(data))
		if progress != nil {
			progress(rep)
		}
	}
	return rep, nil
}
