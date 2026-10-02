package archivebackup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/emit"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/archivebundle"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

const (
	// DefaultMaxPartBytes membatasi ukuran satu bagian bundel terkompres,
	// jauh di bawah batas baca 64 MiB adapter S3. Satu hari prakiraan penuh
	// diperkirakan ±10 MiB, jadi bagian kedua hanya muncul bila ada lonjakan.
	DefaultMaxPartBytes = 48 << 20
	// DefaultGrace menunda penutupan hari: payload yang diambil 23.59.59 UTC
	// bisa baru tertulis beberapa detik setelah tengah malam.
	DefaultGrace = 15 * time.Minute
)

// Backup mengemas arsip panas ke penyimpanan cadangan.
type Backup struct {
	Src ports.ArchiveReader
	Dst ports.ArchiveStore
	Now func() time.Time
	// MaxPartBytes nol = DefaultMaxPartBytes; Grace nol = DefaultGrace.
	MaxPartBytes int
	Grace        time.Duration
	// Progress (boleh nil) dipanggil setelah setiap bagian ditulis.
	Progress func(PartReport)
}

// PartReport adalah satu bagian bundel yang ditulis.
type PartReport struct {
	Connector    string `json:"connector"`
	Day          string `json:"day"`
	Part         int    `json:"part"`
	Objects      int    `json:"objects"`
	PayloadBytes int64  `json:"payload_bytes"`
	BundleBytes  int64  `json:"bundle_bytes"`
}

// BackupReport adalah hasil Backup.Run.
type BackupReport struct {
	Parts []PartReport `json:"parts"`
	// Covered adalah konektor-hari yang sudah lengkap di cadangan.
	Covered int `json:"covered"`
	// Corrupt adalah objek arsip panas rusak yang tidak dicadangkan.
	Corrupt int      `json:"corrupt"`
	Foreign int      `json:"foreign"`
	Samples []string `json:"samples,omitempty"`
	// ClosedBefore: hanya hari sebelum tanggal ini yang dicadangkan.
	ClosedBefore string `json:"closed_before"`
}

// Run mencadangkan setiap konektor-hari yang sudah ditutup dan belum lengkap
// di cadangan. Aman diulang dan dilanjutkan setelah terputus: hari yang
// jumlah objeknya sudah sama dengan jumlah di nama indeks dilewati tanpa
// mengunduh apa pun; selain itu indeks hari itu dibaca dan hanya objek yang
// belum tercakup dikemas sebagai bagian baru.
func (b Backup) Run(ctx context.Context, f Filter) (BackupReport, error) {
	var rep BackupReport
	if b.Now == nil {
		return rep, errors.New("archivebackup: Now kosong")
	}
	grace := b.Grace
	if grace == 0 {
		grace = DefaultGrace
	}
	closed := archivebundle.Day(b.Now().Add(-grace))
	rep.ClosedBefore = dayString(closed)
	hot, err := scanHot(ctx, b.Src, f, func(dk dayKey) bool { return dk.day.Before(closed) })
	if err != nil {
		return rep, err
	}
	rep.Foreign = hot.foreign
	backups := map[string]map[dayKey]*backupDay{}
	for _, dk := range hot.sorted() {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		have, ok := backups[dk.connector]
		if !ok {
			if have, err = scanBackup(ctx, b.Dst, dk.connector+"/"); err != nil {
				return rep, err
			}
			backups[dk.connector] = have
		}
		bd := have[dk]
		if bd == nil {
			bd = &backupDay{}
		}
		objs := hot.days[dk]
		if bd.objects > 0 && bd.objects == len(objs) {
			rep.Covered++
			continue
		}
		covered, _, err := loadIndexes(ctx, b.Dst, bd.indexes)
		if err != nil {
			return rep, err
		}
		var missing []ports.ArchiveObject
		for _, o := range objs {
			if _, ok := covered[o.Key]; !ok {
				missing = append(missing, o)
			}
		}
		if len(missing) == 0 {
			rep.Covered++
			continue
		}
		if err := b.writeDay(ctx, dk, bd.maxPart+1, missing, &rep); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func (b Backup) writeDay(ctx context.Context, dk dayKey, part int, objs []ports.ArchiveObject, rep *BackupReport) error {
	maxPart := b.MaxPartBytes
	if maxPart == 0 {
		maxPart = DefaultMaxPartBytes
	}
	w, err := newBundleWriter()
	if err != nil {
		return err
	}
	for _, o := range objs {
		data, err := b.Src.Get(ctx, o.Key)
		if err != nil {
			return fmt.Errorf("membaca %s: %w", o.Key, err)
		}
		k, body, sum, err := emit.Unarchive(o.Key, data, emit.DefaultMaxPayload)
		if err != nil {
			rep.Corrupt++
			rep.Samples = addSample(rep.Samples, err)
			continue
		}
		if err := w.add(o.Key, k, body, sum); err != nil {
			return err
		}
		if w.compressed() >= maxPart {
			if err := b.writePart(ctx, dk, part, w, rep); err != nil {
				return err
			}
			part++
			if w, err = newBundleWriter(); err != nil {
				return err
			}
		}
	}
	if len(w.entries) == 0 {
		return nil
	}
	return b.writePart(ctx, dk, part, w, rep)
}

// writePart menulis bundel, membacanya ulang, lalu menulis indeks. Indeks
// ditulis terakhir: indeks yang ada selalu menunjuk bundel yang utuh.
func (b Backup) writePart(ctx context.Context, dk dayKey, part int, w *bundleWriter, rep *BackupReport) error {
	data, err := w.finish()
	if err != nil {
		return err
	}
	bundleKey := archivebundle.BundleKey(dk.connector, dk.day, part)
	if err := b.Dst.Put(ctx, bundleKey, data); err != nil {
		return fmt.Errorf("menulis %s: %w", bundleKey, err)
	}
	back, err := b.Dst.Get(ctx, bundleKey)
	if err != nil {
		return fmt.Errorf("membaca ulang %s: %w", bundleKey, err)
	}
	if !bytes.Equal(back, data) {
		return fmt.Errorf("%w: %s berbeda setelah dibaca ulang", ErrCorrupt, bundleKey)
	}
	ix := archivebundle.Index{
		Format: archivebundle.Format, Connector: dk.connector, Day: dayString(dk.day), Part: part,
		CreatedAt:   b.Now().UTC().Truncate(time.Second),
		BundleBytes: int64(len(data)), BundleSHA256: sha256Hex(data),
		Entries: w.entries,
	}
	name, err := ix.Name()
	if err != nil {
		return err
	}
	if err := ix.Validate(name); err != nil {
		return err
	}
	enc, err := encodeIndex(ix)
	if err != nil {
		return err
	}
	if err := b.Dst.Put(ctx, name.String(), enc); err != nil {
		return fmt.Errorf("menulis %s: %w", name, err)
	}
	pr := PartReport{
		Connector: dk.connector, Day: ix.Day, Part: part, Objects: len(ix.Entries),
		PayloadBytes: w.payload, BundleBytes: ix.BundleBytes,
	}
	rep.Parts = append(rep.Parts, pr)
	if b.Progress != nil {
		b.Progress(pr)
	}
	return nil
}
