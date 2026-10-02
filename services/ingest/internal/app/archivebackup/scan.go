package archivebackup

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/archivebundle"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/archivekey"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// MaxSamples membatasi contoh galat di laporan.
const MaxSamples = 20

// Filter membatasi konektor dan rentang hari UTC (inklusif) yang diproses.
// Nilai nol berarti tanpa batas.
type Filter struct {
	Connectors []string
	From, To   time.Time
}

func (f Filter) connector(c string) bool {
	return len(f.Connectors) == 0 || slices.Contains(f.Connectors, c)
}

func (f Filter) day(d time.Time) bool {
	d = archivebundle.Day(d)
	return (f.From.IsZero() || !d.Before(archivebundle.Day(f.From))) &&
		(f.To.IsZero() || !d.After(archivebundle.Day(f.To)))
}

// dayKey adalah satu konektor pada satu hari UTC.
type dayKey struct {
	connector string
	day       time.Time
}

func compareDayKey(a, b dayKey) int {
	return cmp.Or(cmp.Compare(a.connector, b.connector), a.day.Compare(b.day))
}

// hotDays mengelompokkan objek arsip panas per konektor per hari, urut kunci.
type hotDays struct {
	days    map[dayKey][]ports.ArchiveObject
	foreign int
}

func (h hotDays) sorted() []dayKey {
	return slices.SortedFunc(maps.Keys(h.days), compareDayKey)
}

// scanHot mendaftar arsip panas dan mengelompokkannya. keep memilih hari yang
// dipakai; objek berkunci tidak baku dihitung foreign dan tidak pernah
// dicadangkan atau dihapus.
func scanHot(ctx context.Context, src ports.ArchiveReader, f Filter, keep func(dayKey) bool) (hotDays, error) {
	h := hotDays{days: map[dayKey][]ports.ArchiveObject{}}
	for o, err := range src.List(ctx, "", "") {
		if err != nil {
			return h, fmt.Errorf("daftar arsip: %w", err)
		}
		k, err := archivekey.Parse(o.Key)
		if err != nil {
			h.foreign++
			continue
		}
		dk := dayKey{connector: k.Connector, day: archivebundle.Day(k.FetchedAt)}
		if !f.connector(dk.connector) || !f.day(dk.day) || (keep != nil && !keep(dk)) {
			continue
		}
		h.days[dk] = append(h.days[dk], o)
	}
	return h, nil
}

// backupDay adalah isi cadangan satu konektor pada satu hari menurut daftar
// kunci saja.
type backupDay struct {
	indexes []archivebundle.Name
	maxPart int
	objects int
}

// scanBackup mendaftar kunci cadangan berawalan prefix (misal "konektor/")
// dan mengelompokkannya per hari. Kunci tidak baku diabaikan.
func scanBackup(ctx context.Context, dst ports.ArchiveReader, prefix string) (map[dayKey]*backupDay, error) {
	out := map[dayKey]*backupDay{}
	for o, err := range dst.List(ctx, prefix, "") {
		if err != nil {
			return nil, fmt.Errorf("daftar cadangan: %w", err)
		}
		n, err := archivebundle.Parse(o.Key)
		if err != nil {
			continue
		}
		dk := dayKey{connector: n.Connector, day: n.Day}
		bd := out[dk]
		if bd == nil {
			bd = &backupDay{}
			out[dk] = bd
		}
		bd.maxPart = max(bd.maxPart, n.Part)
		if n.Kind == archivebundle.KindIndex {
			bd.indexes = append(bd.indexes, n)
			bd.objects += n.Objects
		}
	}
	return out, nil
}

// loadIndexes membaca semua indeks satu hari dan mengembalikan entri yang
// tercakup per kunci arsip.
func loadIndexes(ctx context.Context, dst ports.ArchiveReader, names []archivebundle.Name) (map[string]archivebundle.Entry, []archivebundle.Index, error) {
	covered := map[string]archivebundle.Entry{}
	var ixs []archivebundle.Index
	for _, n := range names {
		data, err := dst.Get(ctx, n.String())
		if err != nil {
			return nil, nil, fmt.Errorf("membaca indeks %s: %w", n, err)
		}
		ix, err := decodeIndex(data, n)
		if err != nil {
			return nil, nil, err
		}
		for _, e := range ix.Entries {
			covered[e.Key] = e
		}
		ixs = append(ixs, ix)
	}
	return covered, ixs, nil
}

// scanIndexes mendaftar indeks cadangan yang lolos filter, urut kunci.
func scanIndexes(ctx context.Context, dst ports.ArchiveReader, f Filter) ([]archivebundle.Name, error) {
	var out []archivebundle.Name
	prefixes := []string{""}
	if len(f.Connectors) > 0 {
		prefixes = nil
		for _, c := range slices.Sorted(slices.Values(f.Connectors)) {
			prefixes = append(prefixes, c+"/")
		}
	}
	for _, p := range prefixes {
		for o, err := range dst.List(ctx, p, "") {
			if err != nil {
				return nil, fmt.Errorf("daftar cadangan: %w", err)
			}
			n, err := archivebundle.Parse(o.Key)
			if err != nil || n.Kind != archivebundle.KindIndex || !f.connector(n.Connector) || !f.day(n.Day) {
				continue
			}
			out = append(out, n)
		}
	}
	slices.SortFunc(out, func(a, b archivebundle.Name) int { return strings.Compare(a.String(), b.String()) })
	return out, nil
}

func addSample(samples []string, err error) []string {
	if len(samples) < MaxSamples {
		samples = append(samples, err.Error())
	}
	return samples
}
