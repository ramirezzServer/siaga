package archivebackup

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/archivebundle"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/retention"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// PrunableArchive adalah arsip panas yang bisa dipangkas.
type PrunableArchive interface {
	ports.ArchiveReader
	ports.ArchiveDeleter
}

// Prune memangkas arsip panas menurut aturan retensi.
type Prune struct {
	Src    PrunableArchive
	Backup ports.ArchiveReader
	Policy retention.Policy
	Now    func() time.Time
	// Apply false hanya menghitung (bawaan perintah archive prune).
	Apply bool
	// MaxDelete membatasi penghapusan per jalan; nol = tanpa batas.
	MaxDelete int
}

// ConnectorPrune adalah hasil Prune satu konektor.
type ConnectorPrune struct {
	Connector string `json:"connector"`
	KeepDays  int    `json:"keep_days"`
	Cutoff    string `json:"cutoff"`
	Days      int    `json:"days"`
	// Deleted adalah objek yang dihapus (atau akan dihapus bila Apply false).
	Deleted int   `json:"deleted"`
	Bytes   int64 `json:"bytes"`
	// NotBackedUp adalah objek kedaluwarsa yang tidak ada di indeks
	// cadangan, jadi tidak dihapus.
	NotBackedUp int `json:"not_backed_up"`
}

// PruneReport adalah hasil Prune.Run.
type PruneReport struct {
	Applied    bool             `json:"applied"`
	Connectors []ConnectorPrune `json:"connectors"`
	// Limited true bila MaxDelete tercapai sebelum semua selesai.
	Limited bool     `json:"limited"`
	Samples []string `json:"samples,omitempty"`
}

// ErrPolicy menandai aturan retensi yang tidak boleh dipakai memangkas.
var ErrPolicy = errors.New("aturan retensi ditolak")

// Run menghapus objek arsip panas yang kedaluwarsa menurut Policy, hanya bila
// kuncinya tercatat di indeks cadangan hari yang sama (indeks ditulis setelah
// bundelnya dibaca ulang dan cocok, lihat Backup). Objek kedaluwarsa yang
// belum dicadangkan dibiarkan dan dilaporkan. Konektor tanpa aturan tidak
// pernah disentuh.
func (p Prune) Run(ctx context.Context, f Filter) (PruneReport, error) {
	rep := PruneReport{Applied: p.Apply}
	if p.Now == nil {
		return rep, errors.New("archivebackup: Now kosong")
	}
	if err := p.Policy.Validate(); err != nil {
		return rep, fmt.Errorf("%w: %w", ErrPolicy, err)
	}
	now := p.Now()
	hot, err := scanHot(ctx, p.Src, f, func(dk dayKey) bool { return p.Policy.Expired(dk.connector, dk.day, now) })
	if err != nil {
		return rep, err
	}
	byConn := map[string]*ConnectorPrune{}
	deleted := 0
	for _, dk := range hot.sorted() {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		cp := byConn[dk.connector]
		if cp == nil {
			days, _ := p.Policy.Days(dk.connector)
			cutoff, _ := p.Policy.Cutoff(dk.connector, now)
			cp = &ConnectorPrune{Connector: dk.connector, KeepDays: days, Cutoff: dayString(cutoff)}
			byConn[dk.connector] = cp
		}
		cp.Days++
		bd, err := scanBackup(ctx, p.Backup, archivebundle.DayPrefix(dk.connector, dk.day))
		if err != nil {
			return rep, err
		}
		var names []archivebundle.Name
		if d := bd[dk]; d != nil {
			names = d.indexes
		}
		covered, _, err := loadIndexes(ctx, p.Backup, names)
		if err != nil {
			return rep, err
		}
		for _, o := range hot.days[dk] {
			if _, ok := covered[o.Key]; !ok {
				cp.NotBackedUp++
				rep.Samples = addSample(rep.Samples, fmt.Errorf("belum dicadangkan: %s", o.Key))
				continue
			}
			if p.MaxDelete > 0 && deleted >= p.MaxDelete {
				rep.Limited = true
				break
			}
			if p.Apply {
				if err := p.Src.Delete(ctx, o.Key); err != nil {
					return rep, fmt.Errorf("menghapus %s: %w", o.Key, err)
				}
			}
			deleted++
			cp.Deleted++
			cp.Bytes += o.Size
		}
		if rep.Limited {
			break
		}
	}
	for _, c := range slices.Sorted(maps.Keys(byConn)) {
		rep.Connectors = append(rep.Connectors, *byConn[c])
	}
	return rep, nil
}
