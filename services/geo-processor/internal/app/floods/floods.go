// Package floods adalah use case potensi banjir geo-processor: menilai setiap
// keluaran model debit (raw.flood.openmeteo) dan hujan sub-DAS
// (raw.rain.openmeteo) terhadap ambangnya, membuka, memperbarui, dan
// mengakhiri kejadian per titik, dan menulis pesan hazard.flood.* ke outbox
// dalam satu transaksi (ADR 0020–0021).
//
// Satu titik punya paling banyak satu kejadian aktif. Kejadian dibuka
// keluaran pertama yang mencapai ambang Info di jendela hari ini sampai +3
// hari, mengikuti tingkat keluaran terbaru (paling rendah Info selama aktif),
// dan berakhir 24 jam setelah keluaran terakhir yang mencapai Info.
package floods

import (
	"context"
	"fmt"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/flood"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// Service menilai keluaran model dan mengelola kejadian banjir.
type Service struct {
	store  ports.FloodStore
	enc    ports.FloodEncoder
	policy flood.Policy
	cal    flood.Calibration
	now    func() time.Time
}

// New membuat Service. cal harus dibentuk dengan policy yang sama.
func New(store ports.FloodStore, enc ports.FloodEncoder, policy flood.Policy, cal flood.Calibration, now func() time.Time) (*Service, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if err := cal.Validate(); err != nil {
		return nil, err
	}
	return &Service{store: store, enc: enc, policy: policy, cal: cal, now: now}, nil
}

// Calibration mengembalikan ambang yang dipakai.
func (s *Service) Calibration() flood.Calibration { return s.cal }

// Result merangkum dampak satu keluaran model.
type Result struct {
	// Changed false berarti keluaran ulangan atau lebih tua dari yang sudah dinilai.
	Changed bool
	Created int
	Updated int
	Ended   int
	// Level adalah tingkat keluaran ini (flood.None bila di bawah Info).
	Level hazard.Level
}

// Discharge menilai satu keluaran debit. Galat yang membungkus
// series.ErrInvalid atau flood.ErrInvalid tidak akan berhasil bila diulang.
func (s *Service) Discharge(ctx context.Context, run series.DischargeRun) (Result, error) {
	if err := run.Validate(s.now()); err != nil {
		return Result{}, err
	}
	th, ok := s.cal.Discharge[run.Site.ID]
	if !ok {
		return Result{}, fmt.Errorf("%w: %w: %s", flood.ErrInvalid, flood.ErrNoThreshold, run.Site.ID)
	}
	o, err := s.policy.AssessDischarge(run, th)
	if err != nil {
		return Result{}, err
	}
	return s.apply(ctx, o)
}

// Rainfall menilai satu keluaran hujan rata-rata wilayah sub-DAS.
func (s *Service) Rainfall(ctx context.Context, run series.WeatherRun) (Result, error) {
	if err := run.Validate(s.now()); err != nil {
		return Result{}, err
	}
	if run.Site.Kind != series.SiteCatchment {
		return Result{}, fmt.Errorf("%w: indeks hujan untuk titik %s", flood.ErrInvalid, run.Site.ID)
	}
	th, ok := s.cal.Rainfall[run.Site.ID]
	if !ok {
		return Result{}, fmt.Errorf("%w: %w: %s", flood.ErrInvalid, flood.ErrNoThreshold, run.Site.ID)
	}
	o, err := s.policy.AssessRainfall(run, th)
	if err != nil {
		return Result{}, err
	}
	return s.apply(ctx, o)
}

// apply menerapkan satu penilaian. Hanya bergantung pada urutan waktu terima
// keluaran: keluaran yang tidak lebih baru dari yang terakhir dinilai
// diabaikan, jadi pesan ulangan tidak mengubah apa pun.
func (s *Service) apply(ctx context.Context, o flood.Outlook) (Result, error) {
	var res Result
	err := s.store.InTx(ctx, func(tx ports.FloodTx) error {
		res = Result{Level: o.Level}
		if err := tx.LockFlood(ctx); err != nil {
			return err
		}
		st, seen, err := tx.Site(ctx, o.SiteID)
		if err != nil {
			return err
		}
		if seen && !o.FetchedAt.After(st.LastFetchedAt) {
			return nil
		}
		res.Changed = true
		now := s.now().UTC()
		event := st.EventID
		var ep ports.FloodEpisode
		open := false
		if !event.IsZero() {
			var found bool
			if ep, found, err = tx.Episode(ctx, event); err != nil {
				return err
			}
			if found && ep.Status == ports.StatusActive {
				if ep.ExpiresAt.After(o.FetchedAt) {
					open = true
				} else if err := s.end(ctx, tx, ep.ID, ep.Revision+1, now, &res); err != nil {
					// Masa aktif sudah habis sebelum keluaran ini diterima
					// (putaran kedaluwarsa belum sempat jalan).
					return err
				}
			}
		}
		switch exceeded := o.Level != flood.None; {
		case open:
			last := ep.LastExceededAt
			if exceeded {
				last = o.FetchedAt
			}
			if err := s.update(ctx, tx, s.policy.Assess(ep.ID, o, ep.OpenedAt, last), ep, &res); err != nil {
				return err
			}
		case exceeded:
			event = flood.EventIDFor(o.SiteID, o.FetchedAt)
			if err := s.create(ctx, tx, s.policy.Assess(event, o, o.FetchedAt, o.FetchedAt), now, &res); err != nil {
				return err
			}
		}
		return tx.SaveSite(ctx, o.SiteID, o.FetchedAt, event)
	})
	if err != nil {
		return Result{}, fmt.Errorf("menilai banjir %s: %w", o.SiteID, err)
	}
	return res, nil
}

func (s *Service) create(ctx context.Context, tx ports.FloodTx, a flood.Assessment, now time.Time, res *Result) error {
	if err := tx.SaveEvent(ctx, ports.FloodEventRecord{Assessment: a, Revision: 1, Digest: a.Digest()}); err != nil {
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
	// Keluaran lama (misal replay) yang masa aktifnya sudah habis tetap
	// tercatat lengkap: konsumen selalu melihat created sebelum expired.
	if !a.ExpiresAt.After(now) {
		return s.end(ctx, tx, a.ID, 2, now, res)
	}
	return nil
}

func (s *Service) update(ctx context.Context, tx ports.FloodTx, a flood.Assessment, ep ports.FloodEpisode, res *Result) error {
	digest := a.Digest()
	if digest == ep.Digest {
		return nil
	}
	rev := ep.Revision + 1
	if err := tx.SaveEvent(ctx, ports.FloodEventRecord{Assessment: a, Revision: rev, Digest: digest}); err != nil {
		return err
	}
	msg, err := s.enc.Updated(a, rev, ep.Level)
	if err != nil {
		return err
	}
	res.Updated++
	return tx.Enqueue(ctx, msg)
}

func (s *Service) end(ctx context.Context, tx ports.FloodTx, id hazard.EventID, rev int, now time.Time, res *Result) error {
	if err := tx.EndEvent(ctx, id, ports.StatusExpired, hazard.EventID{}, now, rev); err != nil {
		return err
	}
	msg, err := s.enc.Expired(id, now, ports.ExpiryElapsed, hazard.EventID{}, rev)
	if err != nil {
		return err
	}
	res.Ended++
	return tx.Enqueue(ctx, msg)
}

// Expire mengakhiri kejadian banjir aktif yang masa aktifnya habis pada now.
// Mengembalikan jumlah kejadian yang diakhiri.
func (s *Service) Expire(ctx context.Context, now time.Time, limit int) (int, error) {
	n := 0
	err := s.store.InTx(ctx, func(tx ports.FloodTx) error {
		n = 0
		if err := tx.LockFlood(ctx); err != nil {
			return err
		}
		due, err := tx.DueForExpiry(ctx, now, limit)
		if err != nil {
			return err
		}
		for _, ev := range due {
			var res Result
			if err := s.end(ctx, tx, ev.ID, ev.Revision+1, now.UTC(), &res); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("mengakhiri kejadian banjir kedaluwarsa: %w", err)
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

// SyncThresholds menulis ambang kalibrasi ke schema ref.
func (s *Service) SyncThresholds(ctx context.Context) (ports.ThresholdSync, error) {
	return s.store.SyncThresholds(ctx, s.cal)
}
