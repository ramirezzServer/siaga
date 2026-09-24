// Package warnings adalah use case peringatan dini cuaca geo-processor:
// menyimpan pesan CAP dari raw.weather.*, menyusun rantai pembaruannya
// (references) menjadi satu kejadian, menghitung wilayah terdampak dari
// poligonnya, dan menulis pesan hazard.weather.* ke outbox dalam satu transaksi.
package warnings

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/weather"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// Service memproses pesan CAP dan kedaluwarsa kejadian cuaca.
type Service struct {
	store  ports.WeatherStore
	enc    ports.WeatherEncoder
	policy weather.Policy
	now    func() time.Time
}

// New membuat Service. now menentukan apakah peringatan masih berlaku saat
// diproses dan kapan kejadian diakhiri.
func New(store ports.WeatherStore, enc ports.WeatherEncoder, policy weather.Policy, now func() time.Time) (*Service, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &Service{store: store, enc: enc, policy: policy, now: now}, nil
}

// Result merangkum dampak satu pesan.
type Result struct {
	// Changed false berarti pesan ulangan atau revisi yang lebih lama.
	Changed bool
	Created int
	Updated int
	Ended   int
	// Ignored true bila area peringatan tidak beririsan dengan wilayah pantauan.
	Ignored bool
}

// Process menerapkan satu pesan. Aman dipanggil berulang untuk pesan yang
// sama dan dalam urutan apa pun: isi kejadian hanya bergantung pada himpunan
// pesan yang pernah diterima dan waktu pemrosesan. Galat yang membungkus
// weather.ErrInvalid tidak akan berhasil bila diulang.
func (s *Service) Process(ctx context.Context, m weather.Message) (Result, error) {
	m.Digest = m.ContentDigest()
	if err := m.Validate(); err != nil {
		return Result{}, err
	}
	var res Result
	err := s.store.InTx(ctx, func(tx ports.WeatherTx) error {
		res = Result{}
		if err := tx.LockWeather(ctx); err != nil {
			return err
		}
		prev, err := tx.Message(ctx, m.Key)
		if err != nil {
			return err
		}
		if prev != nil {
			if prev.Digest == m.Digest || !weather.Supersedes(m, *prev) {
				return nil
			}
			m.FirstSeenAt = minTime(m.FirstSeenAt, prev.FirstSeenAt)
		}
		if err := tx.SaveMessage(ctx, m); err != nil {
			return err
		}
		res.Changed = true
		chain, err := tx.Component(ctx, m.Key)
		if err != nil {
			return err
		}
		return s.settle(ctx, tx, chain, &res)
	})
	if err != nil {
		return Result{}, fmt.Errorf("memproses %s: %w", m.Key, err)
	}
	return res, nil
}

// settle menyelaraskan kejadian rantai dengan pesan-pesannya.
func (s *Service) settle(ctx context.Context, tx ports.WeatherTx, chain []weather.Message, res *Result) error {
	view, err := weather.Derive(chain)
	if errors.Is(err, weather.ErrNoArea) {
		return nil // misal Cancel untuk peringatan yang belum pernah diterima
	}
	if err != nil {
		return err
	}
	fp, err := tx.Footprint(ctx, view.Content.Key)
	if err != nil {
		return err
	}
	a := s.policy.Assess(view, fp)
	// Kejadian lain yang pernah didirikan pesan rantai ini (rantai yang baru
	// tersambung lewat references, atau pendiri yang bergeser) digabung ke
	// kejadian rantai.
	var homes []hazard.EventID
	keys := make([]weather.Key, len(chain))
	for i, m := range chain {
		keys[i] = m.Key
		if !m.Event.IsZero() && m.Event != a.ID && !slices.Contains(homes, m.Event) {
			homes = append(homes, m.Event)
		}
	}
	slices.SortFunc(homes, hazard.EventID.Compare)
	prev, exists, err := tx.Event(ctx, a.ID)
	if err != nil {
		return err
	}
	// Rantai di luar wilayah pantauan tidak membentuk kejadian, kecuali
	// kelanjutan rantai yang sudah punya kejadian.
	if !exists && !a.Relevant && len(homes) == 0 {
		res.Ignored = true
		return nil
	}
	now := s.now().UTC()
	if err := s.apply(ctx, tx, a, prev, exists, now, res); err != nil {
		return err
	}
	for _, h := range homes {
		if err := s.merge(ctx, tx, h, a.ID, now, res); err != nil {
			return err
		}
	}
	return tx.SetMembership(ctx, a.ID, keys)
}

// apply menulis kejadian dan menerbitkan transisinya.
func (s *Service) apply(ctx context.Context, tx ports.WeatherTx, a weather.Assessment, prev ports.StoredEvent, exists bool, now time.Time, res *Result) error {
	digest := a.Digest()
	desired := a.Status(now)
	if !exists {
		if err := tx.SaveEvent(ctx, ports.WeatherEventRecord{Assessment: a, Revision: 1, Digest: digest}); err != nil {
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
		// Peringatan yang sudah berakhir saat pertama diproses (misal replay)
		// tetap tercatat lengkap: konsumen selalu melihat created sebelum expired.
		if desired != weather.StatusActive {
			return s.end(ctx, tx, a.ID, desired, 2, now, res)
		}
		return nil
	}
	changed := prev.Digest != digest
	active := prev.Status == ports.StatusActive
	rev := prev.Revision + 1
	switch {
	case desired == weather.StatusActive && (!active || changed):
		if err := tx.SaveEvent(ctx, ports.WeatherEventRecord{Assessment: a, Revision: rev, Digest: digest}); err != nil {
			return err
		}
		if !active {
			// Termasuk kejadian yang pernah digabung: pendiri rantai bisa
			// kembali ke node lama bila rujukan dengan waktu kirim lebih awal
			// datang belakangan (ADR 0010).
			if err := tx.Reactivate(ctx, a.ID); err != nil {
				return err
			}
		}
		msg, err := s.enc.Updated(a, rev, prev.Level)
		if err != nil {
			return err
		}
		res.Updated++
		return tx.Enqueue(ctx, msg)
	case desired != weather.StatusActive && active:
		if changed {
			if err := tx.SaveEvent(ctx, ports.WeatherEventRecord{Assessment: a, Revision: rev, Digest: digest}); err != nil {
				return err
			}
		}
		return s.end(ctx, tx, a.ID, desired, rev, now, res)
	case desired == weather.StatusActive:
		return nil // aktif dan isinya tidak berubah
	default:
		// Kejadian yang sudah berakhir (termasuk yang pernah digabung lalu
		// kembali menjadi kejadian rantai) dan tetap berakhir: koreksi isi dan
		// status disimpan untuk riwayat tanpa diterbitkan, karena konsumen
		// sudah menerima expired untuknya. Status mengikuti rantai (misal
		// Cancel yang tiba setelah masa berlaku habis → retracted), supaya
		// keadaan akhir tidak bergantung urutan kedatangan.
		st := endStatus(desired)
		if !changed && prev.Status == st {
			return nil
		}
		if err := tx.SaveEvent(ctx, ports.WeatherEventRecord{Assessment: a, Revision: rev, Digest: digest}); err != nil {
			return err
		}
		if prev.Status == st {
			return nil
		}
		return tx.EndEvent(ctx, a.ID, st, hazard.EventID{}, now, rev)
	}
}

func endStatus(s weather.Status) ports.EventStatus {
	if s == weather.StatusRetracted {
		return ports.StatusRetracted
	}
	return ports.StatusExpired
}

func (s *Service) end(ctx context.Context, tx ports.WeatherTx, id hazard.EventID, status weather.Status, rev int, now time.Time, res *Result) error {
	st, reason := endStatus(status), ports.ExpiryElapsed
	if st == ports.StatusRetracted {
		reason = ports.ExpiryRetracted
	}
	if err := tx.EndEvent(ctx, id, st, hazard.EventID{}, now, rev); err != nil {
		return err
	}
	msg, err := s.enc.Expired(id, now, reason, hazard.EventID{}, rev)
	if err != nil {
		return err
	}
	res.Ended++
	return tx.Enqueue(ctx, msg)
}

func (s *Service) merge(ctx context.Context, tx ports.WeatherTx, id, into hazard.EventID, now time.Time, res *Result) error {
	ev, ok, err := tx.Event(ctx, id)
	if err != nil || !ok || ev.Status == ports.StatusMerged {
		return err
	}
	rev := ev.Revision + 1
	if err := tx.EndEvent(ctx, id, ports.StatusMerged, into, now, rev); err != nil {
		return err
	}
	if ev.Status != ports.StatusActive {
		return nil // konsumen sudah tahu kejadian ini berakhir
	}
	msg, err := s.enc.Expired(id, now, ports.ExpiryMerged, into, rev)
	if err != nil {
		return err
	}
	res.Ended++
	return tx.Enqueue(ctx, msg)
}

// Expire mengakhiri kejadian cuaca aktif yang masa berlakunya habis pada
// now. Mengembalikan jumlah kejadian yang diakhiri.
func (s *Service) Expire(ctx context.Context, now time.Time, limit int) (int, error) {
	n := 0
	err := s.store.InTx(ctx, func(tx ports.WeatherTx) error {
		n = 0
		if err := tx.LockWeather(ctx); err != nil {
			return err
		}
		due, err := tx.DueForExpiry(ctx, now, limit)
		if err != nil {
			return err
		}
		for _, ev := range due {
			var res Result
			if err := s.end(ctx, tx, ev.ID, weather.StatusExpired, ev.Revision+1, now.UTC(), &res); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("mengakhiri peringatan kedaluwarsa: %w", err)
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

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}
