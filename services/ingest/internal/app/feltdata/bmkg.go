package feltdata

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/archivekey"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// FeltConnector adalah konektor arsip feed gempa dirasakan BMKG.
const FeltConnector = "bmkg-gempadirasakan"

// ParseFunc mengurai satu payload gempadirasakan menjadi laporan.
type ParseFunc func(body []byte, fetchedAt time.Time) ([]quake.Report, []ports.Rejection, error)

// BMKGFelt adalah satu gempa dirasakan BMKG: isi payload terakhir yang
// memuatnya (BMKG bisa menambah wilayah dirasakan setelah terbit).
type BMKGFelt struct {
	quake.Report
	FirstSeen, LastSeen time.Time
	// Versions adalah jumlah isi "Dirasakan" berbeda selama terlihat.
	Versions int
}

// BMKGStats merangkum CollectBMKG.
type BMKGStats struct {
	Payloads, Unreadable, Rejected, Events, Revised int
}

// CollectBMKG membaca semua payload gempadirasakan di arsip yang diambil
// sebelum until (nol = tanpa batas), urut waktu ambil. Payload yang rusak
// atau tidak terbaca dihitung, tidak menghentikan baca.
func CollectBMKG(ctx context.Context, ar ports.ArchiveReader, parse ParseFunc, until time.Time) ([]BMKGFelt, BMKGStats, error) {
	var st BMKGStats
	byID := map[string]*BMKGFelt{}
	seenFelt := map[string]map[string]bool{}
	for obj, err := range ar.List(ctx, FeltConnector+"/", "") {
		if err != nil {
			return nil, st, fmt.Errorf("daftar arsip %s: %w", FeltConnector, err)
		}
		k, err := archivekey.Parse(obj.Key)
		if err != nil || k.Connector != FeltConnector {
			st.Unreadable++
			continue
		}
		if !until.IsZero() && !k.FetchedAt.Before(until) {
			break // daftar urut waktu ambil
		}
		gz, err := ar.Get(ctx, obj.Key)
		if err != nil {
			return nil, st, fmt.Errorf("arsip %s: %w", obj.Key, err)
		}
		body, err := gunzip(gz)
		if err != nil {
			st.Unreadable++
			continue
		}
		st.Payloads++
		reports, rejected, err := parse(body, k.FetchedAt)
		if err != nil {
			st.Unreadable++
			continue
		}
		st.Rejected += len(rejected)
		for _, r := range reports {
			e, ok := byID[r.EventID]
			if !ok {
				e = &BMKGFelt{FirstSeen: k.FetchedAt}
				byID[r.EventID] = e
				seenFelt[r.EventID] = map[string]bool{}
			}
			// Daftar arsip urut kunci = urut waktu ambil, jadi payload ini
			// yang terbaru sejauh ini.
			e.Report, e.LastSeen = r, k.FetchedAt
			felt := strings.Join(strings.Fields(r.Felt), " ")
			if !seenFelt[r.EventID][felt] {
				seenFelt[r.EventID][felt] = true
				e.Versions++
			}
		}
	}
	out := make([]BMKGFelt, 0, len(byID))
	for _, e := range byID {
		if e.Versions > 1 {
			st.Revised++
		}
		out = append(out, *e)
	}
	slices.SortFunc(out, func(a, b BMKGFelt) int {
		return cmp.Or(a.OccurredAt.Compare(b.OccurredAt), cmp.Compare(a.EventID, b.EventID))
	})
	st.Events = len(out)
	return out, st, nil
}
