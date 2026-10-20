package archivebackup

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/emit"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/archivebundle"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/archivekey"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// VerifyReport adalah hasil Verify.
type VerifyReport struct {
	// From dan To adalah hari UTC pertama dan terakhir yang diperiksa
	// VerifyLatest (kosong untuk Verify atau bila cadangan kosong).
	From    string   `json:"from,omitempty"`
	To      string   `json:"to,omitempty"`
	Parts   int      `json:"parts"`
	Objects int      `json:"objects"`
	Corrupt int      `json:"corrupt"`
	Samples []string `json:"samples,omitempty"`
}

// Verify mengunduh setiap bagian cadangan yang lolos filter dan memeriksa
// indeks, bundel, dan setiap payload (nama, ukuran, SHA-256 terhadap indeks
// dan potongan di kunci arsip). Bagian rusak dihitung; galat baca
// penyimpanan menghentikan verifikasi.
func Verify(ctx context.Context, dst ports.ArchiveReader, f Filter) (VerifyReport, error) {
	names, err := scanIndexes(ctx, dst, f)
	if err != nil {
		return VerifyReport{}, err
	}
	return verifyParts(ctx, dst, names, VerifyReport{})
}

// VerifyLatest seperti Verify, tetapi hanya untuk days hari UTC terbaru yang
// ada di cadangan (setelah filter konektor dan rentang), bukan hari kalender
// terakhir. Saat ingest mati beberapa hari, hari kalender terakhir tidak
// punya cadangan dan Verify tidak memeriksa apa pun; VerifyLatest tetap
// memeriksa cadangan yang terakhir ditulis.
func VerifyLatest(ctx context.Context, dst ports.ArchiveReader, f Filter, days int) (VerifyReport, error) {
	if days < 1 {
		return VerifyReport{}, errors.New("archivebackup: jumlah hari VerifyLatest harus >= 1")
	}
	names, err := scanIndexes(ctx, dst, f)
	if err != nil {
		return VerifyReport{}, err
	}
	names, from, to := latestDays(names, days)
	rep := VerifyReport{}
	if len(names) > 0 {
		rep.From, rep.To = dayString(from), dayString(to)
	}
	return verifyParts(ctx, dst, names, rep)
}

// latestDays memilih indeks dari days hari berbeda terbaru, urutan asli
// dipertahankan.
func latestDays(names []archivebundle.Name, days int) ([]archivebundle.Name, time.Time, time.Time) {
	distinct := map[time.Time]bool{}
	for _, n := range names {
		distinct[n.Day] = true
	}
	all := slices.SortedFunc(maps.Keys(distinct), func(a, b time.Time) int { return b.Compare(a) })
	if len(all) == 0 {
		return nil, time.Time{}, time.Time{}
	}
	keep := all[:min(days, len(all))]
	from, to := keep[len(keep)-1], keep[0]
	out := slices.DeleteFunc(slices.Clone(names), func(n archivebundle.Name) bool { return n.Day.Before(from) })
	return out, from, to
}

func verifyParts(ctx context.Context, dst ports.ArchiveReader, names []archivebundle.Name, rep VerifyReport) (VerifyReport, error) {
	for _, n := range names {
		rep.Parts++
		ix, data, err := fetchPart(ctx, dst, n)
		if err == nil {
			err = readBundle(data, ix, func(archivebundle.Entry, []byte) error { return nil })
		}
		switch {
		case errors.Is(err, ErrCorrupt):
			rep.Corrupt++
			rep.Samples = addSample(rep.Samples, err)
		case err != nil:
			return rep, err
		default:
			rep.Objects += len(ix.Entries)
		}
	}
	return rep, nil
}

// fetchPart membaca indeks dan bundel satu bagian.
func fetchPart(ctx context.Context, dst ports.ArchiveReader, n archivebundle.Name) (archivebundle.Index, []byte, error) {
	raw, err := dst.Get(ctx, n.String())
	if err != nil {
		return archivebundle.Index{}, nil, fmt.Errorf("membaca indeks %s: %w", n, err)
	}
	ix, err := decodeIndex(raw, n)
	if err != nil {
		return ix, nil, err
	}
	key := archivebundle.BundleKey(n.Connector, n.Day, n.Part)
	data, err := dst.Get(ctx, key)
	if errors.Is(err, ports.ErrArchiveNotFound) {
		return ix, nil, fmt.Errorf("%w: bundel %s hilang", ErrCorrupt, key)
	}
	if err != nil {
		return ix, nil, fmt.Errorf("membaca %s: %w", key, err)
	}
	return ix, data, nil
}

// RestoreReport adalah hasil Restore.
type RestoreReport struct {
	Parts    int      `json:"parts"`
	Restored int      `json:"restored"`
	Existing int      `json:"existing"`
	Corrupt  int      `json:"corrupt"`
	Samples  []string `json:"samples,omitempty"`
}

// Restore menulis kembali payload dari cadangan ke arsip to dengan kunci
// arsip yang sama (gzip seperti emit.Archive), melewati kunci yang sudah ada.
// Bagian rusak dilewati dan dilaporkan; aman diulang.
func Restore(ctx context.Context, dst ports.ArchiveReader, to ports.ArchiveStore, f Filter) (RestoreReport, error) {
	var rep RestoreReport
	names, err := scanIndexes(ctx, dst, f)
	if err != nil {
		return rep, err
	}
	existing := map[string]bool{}
	listed := map[string]bool{}
	for _, n := range names {
		prefix := archivekey.DayPrefix(n.Connector, n.Day)
		if !listed[prefix] {
			listed[prefix] = true
			for o, err := range to.List(ctx, prefix, "") {
				if err != nil {
					return rep, fmt.Errorf("daftar tujuan: %w", err)
				}
				existing[o.Key] = true
			}
		}
		rep.Parts++
		ix, data, err := fetchPart(ctx, dst, n)
		if err == nil {
			err = readBundle(data, ix, func(e archivebundle.Entry, payload []byte) error {
				if existing[e.Key] {
					rep.Existing++
					return nil
				}
				k, err := archivekey.Parse(e.Key)
				if err != nil {
					return fmt.Errorf("%w: %w", ErrCorrupt, err)
				}
				key, err := emit.Archive(ctx, to, k.Connector, k.Ext, e.SHA256, payload, k.FetchedAt)
				if err != nil {
					return err
				}
				if key != e.Key {
					return fmt.Errorf("kunci hasil pulih %s berbeda dari %s", key, e.Key)
				}
				existing[key] = true
				rep.Restored++
				return nil
			})
		}
		switch {
		case errors.Is(err, ErrCorrupt):
			rep.Corrupt++
			rep.Samples = addSample(rep.Samples, err)
		case err != nil:
			return rep, err
		}
	}
	return rep, nil
}
