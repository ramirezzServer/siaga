// Package fires adalah use case titik api geo-processor: setiap deteksi
// raw.fire.firms di luar sumber panas tetap dikelompokkan dengan deteksi
// lain yang berjarak ≤ 2 km dan berselang ≤ 24 jam menjadi satu kejadian,
// tingkatnya dinilai dari semua anggotanya, dan pesan hazard.fire.* ditulis
// ke outbox dalam transaksi yang sama (ADR 0023).
//
// Kejadian dibuka deteksi pertama yang tidak tersambung ke kejadian aktif,
// dua kejadian aktif yang tersambung oleh satu deteksi digabung (yang lebih
// muda berakhir sebagai merged), dan kejadian berakhir 24 jam setelah data
// terakhirnya diterima.
package fires

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hotspot"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// Service mengelola kejadian titik api.
type Service struct {
	store  ports.FireStore
	enc    ports.FireEncoder
	policy fire.Policy
	cal    fire.Calibration
	now    func() time.Time
}

// New membuat Service.
func New(store ports.FireStore, enc ports.FireEncoder, policy fire.Policy, cal fire.Calibration, now func() time.Time) (*Service, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if err := cal.Validate(); err != nil {
		return nil, err
	}
	return &Service{store: store, enc: enc, policy: policy, cal: cal, now: now}, nil
}

// Calibration mengembalikan saringan yang dipakai.
func (s *Service) Calibration() fire.Calibration { return s.cal }

// Skip adalah alasan deteksi tidak dinilai.
type Skip string

// Alasan deteksi tidak dinilai.
const (
	// SkipProduct: produk tidak dinilai (S-NPP).
	SkipProduct Skip = "produk"
	// SkipStatic: deteksi di sumber panas tetap.
	SkipStatic Skip = "sumber-tetap"
	// SkipSeen: deteksi sudah menjadi anggota kejadian (pesan ulangan).
	SkipSeen Skip = "ulangan"
)

// Result merangkum dampak satu deteksi.
type Result struct {
	Changed bool
	Skipped Skip
	// Static adalah ID sumber panas tetap bila Skipped = SkipStatic.
	Static  string
	Created int
	Updated int
	Merged  int
	Ended   int
	Event   hazard.EventID
	Level   hazard.Level
}

// Process menilai satu deteksi. Galat yang membungkus hotspot.ErrInvalid
// atau fire.ErrInvalid tidak akan berhasil bila diulang.
func (s *Service) Process(ctx context.Context, h hotspot.Detection) (Result, error) {
	if err := h.Validate(s.now()); err != nil {
		return Result{}, err
	}
	d := fire.FromHotspot(h)
	if s.policy.Excludes(d.Product) {
		return Result{Skipped: SkipProduct}, nil
	}
	if src, ok := s.cal.Static.Match(d.Lat, d.Lon); ok {
		return Result{Skipped: SkipStatic, Static: src.ID}, nil
	}
	var res Result
	err := s.store.InTx(ctx, func(tx ports.FireTx) error {
		res = Result{}
		if err := tx.LockFire(ctx); err != nil {
			return err
		}
		seen, err := tx.HasDetection(ctx, d.ID)
		if err != nil {
			return err
		}
		if seen {
			res.Skipped = SkipSeen
			return nil
		}
		res.Changed = true
		linked, err := s.linked(ctx, tx, d, &res)
		if err != nil {
			return err
		}
		if len(linked) == 0 {
			return s.open(ctx, tx, d, &res)
		}
		return s.join(ctx, tx, d, linked, &res)
	})
	if err != nil {
		return Result{}, fmt.Errorf("menilai titik api %s: %w", d.ID, err)
	}
	return res, nil
}

// linked mengembalikan kejadian aktif yang tersambung ke d, urut deteksi
// pertama lalu ID (yang pertama bertahan saat digabung). Kejadian yang masa
// aktifnya sudah habis saat d diterima diakhiri dulu (putaran kedaluwarsa
// belum sempat jalan) dan tidak disambung.
func (s *Service) linked(ctx context.Context, tx ports.FireTx, d fire.Detection, res *Result) ([]ports.FireEpisode, error) {
	near, err := tx.Nearby(ctx, d, s.policy.Gap, s.policy.LinkKm+0.5)
	if err != nil {
		return nil, err
	}
	ids := map[hazard.EventID]bool{}
	var order []hazard.EventID
	for _, m := range near {
		if !ids[m.EventID] && s.policy.Linked(d, m.Detection) {
			ids[m.EventID] = true
			order = append(order, m.EventID)
		}
	}
	var out []ports.FireEpisode
	for _, id := range order {
		ep, ok, err := tx.Episode(ctx, id)
		if err != nil {
			return nil, err
		}
		if !ok || ep.Status != ports.StatusActive {
			continue
		}
		if ep.ExpiresAt.Before(d.FetchedAt) {
			if err := s.end(ctx, tx, ep.ID, ports.StatusExpired, hazard.EventID{}, ep.Revision+1, s.now().UTC()); err != nil {
				return nil, err
			}
			res.Ended++
			continue
		}
		out = append(out, ep)
	}
	slices.SortFunc(out, func(a, b ports.FireEpisode) int {
		return cmp.Or(a.FirstDetected.Compare(b.FirstDetected), a.ID.Compare(b.ID))
	})
	return out, nil
}

// open membuka kejadian baru berisi d.
func (s *Service) open(ctx context.Context, tx ports.FireTx, d fire.Detection, res *Result) error {
	id := fire.EventIDFor(d.ID)
	a, err := s.policy.Assess(id, d.ID, []fire.Detection{d})
	if err != nil {
		return err
	}
	res.Event, res.Level = id, a.Level
	if err := tx.SaveEvent(ctx, ports.FireEventRecord{Assessment: a, Revision: 1, Digest: a.Digest()}); err != nil {
		return err
	}
	if err := tx.AddDetection(ctx, id, d); err != nil {
		return err
	}
	msg, err := s.enc.Created(a, 1)
	if err != nil {
		return err
	}
	if err := tx.Enqueue(ctx, msg); err != nil {
		return err
	}
	res.Created++
	// Deteksi lama (replay) yang masa aktifnya sudah habis tetap tercatat
	// lengkap: konsumen selalu melihat created sebelum expired.
	if now := s.now().UTC(); !a.ExpiresAt.After(now) {
		res.Ended++
		return s.end(ctx, tx, id, ports.StatusExpired, hazard.EventID{}, 2, now)
	}
	return nil
}

// join menambahkan d ke kejadian pertama di linked dan menggabungkan
// kejadian lain ke dalamnya.
func (s *Service) join(ctx context.Context, tx ports.FireTx, d fire.Detection, linked []ports.FireEpisode, res *Result) error {
	keep := linked[0]
	now := s.now().UTC()
	for _, other := range linked[1:] {
		if err := tx.MoveDetections(ctx, other.ID, keep.ID); err != nil {
			return err
		}
		if err := s.end(ctx, tx, other.ID, ports.StatusMerged, keep.ID, other.Revision+1, now); err != nil {
			return err
		}
		res.Merged++
	}
	if err := tx.AddDetection(ctx, keep.ID, d); err != nil {
		return err
	}
	members, err := tx.Members(ctx, keep.ID)
	if err != nil {
		return err
	}
	a, err := s.policy.Assess(keep.ID, keep.OpenedBy, members)
	if err != nil {
		return err
	}
	res.Event, res.Level = keep.ID, a.Level
	digest := a.Digest()
	if digest == keep.Digest {
		return nil
	}
	rev := keep.Revision + 1
	if err := tx.SaveEvent(ctx, ports.FireEventRecord{Assessment: a, Revision: rev, Digest: digest}); err != nil {
		return err
	}
	msg, err := s.enc.Updated(a, rev, keep.Level)
	if err != nil {
		return err
	}
	res.Updated++
	return tx.Enqueue(ctx, msg)
}

func (s *Service) end(ctx context.Context, tx ports.FireTx, id hazard.EventID, status ports.EventStatus, into hazard.EventID, rev int, now time.Time) error {
	if err := tx.EndEvent(ctx, id, status, into, now, rev); err != nil {
		return err
	}
	reason := ports.ExpiryElapsed
	if status == ports.StatusMerged {
		reason = ports.ExpiryMerged
	}
	msg, err := s.enc.Expired(id, now, reason, into, rev)
	if err != nil {
		return err
	}
	return tx.Enqueue(ctx, msg)
}

// Expire mengakhiri kejadian titik api aktif yang masa aktifnya habis pada
// now. Mengembalikan jumlah kejadian yang diakhiri.
func (s *Service) Expire(ctx context.Context, now time.Time, limit int) (int, error) {
	n := 0
	err := s.store.InTx(ctx, func(tx ports.FireTx) error {
		n = 0
		if err := tx.LockFire(ctx); err != nil {
			return err
		}
		due, err := tx.DueForExpiry(ctx, now, limit)
		if err != nil {
			return err
		}
		for _, ev := range due {
			if err := s.end(ctx, tx, ev.ID, ports.StatusExpired, hazard.EventID{}, ev.Revision+1, now.UTC()); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("mengakhiri kejadian titik api kedaluwarsa: %w", err)
	}
	return n, nil
}

// RunExpiry menjalankan Expire setiap interval sampai ctx selesai. after
// dipanggil setelah putaran yang mengakhiri kejadian atau gagal.
func (s *Service) RunExpiry(ctx context.Context, interval time.Duration, batch int, after func(n int, err error)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		n, err := s.Expire(ctx, s.now(), batch)
		if after != nil && (n > 0 || err != nil) && ctx.Err() == nil {
			after(n, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// SyncStatic menulis saringan sumber panas tetap ke schema ref.
func (s *Service) SyncStatic(ctx context.Context) (ports.ThresholdSync, error) {
	return s.store.SyncStatic(ctx, s.cal)
}
