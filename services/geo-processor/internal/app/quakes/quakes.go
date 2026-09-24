// Package quakes adalah use case gempa geo-processor: menyimpan laporan dari
// raw.quake.*, mengelompokkannya menjadi kejadian (deduplikasi BMKG–USGS),
// menghitung wilayah terdampak, dan menulis pesan hazard.quake.* ke outbox
// dalam satu transaksi.
package quakes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// Service memproses laporan gempa dan kedaluwarsa kejadian.
type Service struct {
	store  ports.QuakeStore
	enc    ports.HazardEncoder
	rules  quake.Rules
	policy quake.Policy
	now    func() time.Time
}

// New membuat Service. now dipakai hanya untuk waktu pencatatan (misal kapan
// kejadian diakhiri), bukan untuk logika pengelompokan.
func New(store ports.QuakeStore, enc ports.HazardEncoder, rules quake.Rules, policy quake.Policy, now func() time.Time) (*Service, error) {
	if err := errors.Join(rules.Validate(), policy.Validate()); err != nil {
		return nil, err
	}
	return &Service{store: store, enc: enc, rules: rules, policy: policy, now: now}, nil
}

// Result merangkum dampak satu laporan.
type Result struct {
	// Changed false berarti laporan ulangan atau lebih lama dari yang tersimpan.
	Changed bool
	Created int
	Updated int
	Ended   int
}

// Process menerapkan satu laporan. Aman dipanggil berulang untuk laporan yang
// sama dan dalam urutan apa pun: keadaan akhir hanya bergantung pada himpunan
// laporan yang pernah diterima. Galat yang membungkus quake.ErrInvalid tidak
// akan berhasil bila diulang.
func (s *Service) Process(ctx context.Context, r quake.Report) (Result, error) {
	if err := r.Validate(); err != nil {
		return Result{}, err
	}
	var res Result
	err := s.store.InTx(ctx, func(tx ports.QuakeTx) error {
		res = Result{}
		if err := tx.LockQuakes(ctx); err != nil {
			return err
		}
		prev, err := tx.Report(ctx, r.Key())
		if err != nil {
			return err
		}
		// Kejadian asal dibaca sebelum laporan ditulis: laporan yang ditarik
		// sumber langsung lepas dari kejadiannya saat disimpan.
		home, err := tx.EventOf(ctx, r.Identity())
		if err != nil {
			return err
		}
		next, write, changed := quake.Apply(prev, r)
		if write {
			if err := tx.SaveReport(ctx, next); err != nil {
				return err
			}
		}
		if !changed {
			return nil
		}
		res.Changed = true
		return s.recluster(ctx, tx, r.Identity(), home, &res)
	})
	if err != nil {
		return Result{}, fmt.Errorf("memproses %s: %w", r.Key().IdentityKey, err)
	}
	return res, nil
}

// window adalah jarak waktu terjauh dua solusi yang masih bisa cocok.
func (s *Service) window() time.Duration {
	return max(s.rules.CrossSource.MaxTimeDelta, s.rules.Revision.MaxTimeDelta)
}

func (s *Service) recluster(ctx context.Context, tx ports.QuakeTx, key quake.IdentityKey, home quake.EventID, res *Result) error {
	reports, err := tx.IdentityReports(ctx, key)
	if err != nil {
		return err
	}
	self, err := quake.Merge(reports)
	if err != nil {
		return err
	}
	var include []quake.EventID
	if !home.IsZero() {
		include = append(include, home)
	}
	w := s.window()
	raw, err := tx.Clusters(ctx, self.OccurredAt.Add(-w), self.OccurredAt.Add(w), include)
	if err != nil {
		return err
	}
	clusters, err := toClusters(raw)
	if err != nil {
		return err
	}
	if _, ok := raw[home]; !home.IsZero() && !ok {
		// Semua laporan kejadian asal sudah lepas (laporan ditarik sumber).
		clusters = append(clusters, quake.Cluster{ID: home})
	}

	var takenErr error
	taken := func(id quake.EventID) bool {
		if takenErr != nil {
			return false // hasil tidak dipakai; galat dikembalikan setelah Settle
		}
		ok, err := tx.EventExists(ctx, id)
		takenErr = err
		return ok
	}
	out, err := quake.Settle(self, home, clusters, s.rules, taken)
	if err := errors.Join(err, takenErr); err != nil {
		return err
	}

	// Tujuan setiap identitas setelah pengelompokan, untuk mencatat kejadian
	// yang kosong digabung ke mana.
	dest := map[quake.IdentityKey]quake.EventID{}
	for _, ch := range out.Changes {
		for _, m := range ch.Members {
			dest[m.Key] = ch.ID
		}
	}
	if self.Deleted {
		if err := tx.Detach(ctx, self.Key); err != nil {
			return err
		}
	}
	// Keanggotaan ditulis dulu, baru kejadian yang kosong diakhiri.
	for _, ch := range out.Changes {
		if ch.Empty() {
			continue
		}
		if err := s.saveChange(ctx, tx, ch, res); err != nil {
			return err
		}
	}
	for _, ch := range out.Changes {
		if !ch.Empty() {
			continue
		}
		var former []quake.IdentityKey
		for _, c := range clusters {
			if c.ID == ch.ID {
				for _, m := range c.Members {
					former = append(former, m.Key)
				}
			}
		}
		if err := s.end(ctx, tx, ch.ID, mergeTarget(former, dest), res); err != nil {
			return err
		}
	}
	return nil
}

// mergeTarget memilih kejadian tujuan anggota lama kejadian yang kosong
// (anggota pertama menurut kunci), atau kosong bila semua anggotanya ditarik.
func mergeTarget(former []quake.IdentityKey, dest map[quake.IdentityKey]quake.EventID) quake.EventID {
	slices.SortFunc(former, quake.IdentityKey.Compare)
	for _, k := range former {
		if id, ok := dest[k]; ok {
			return id
		}
	}
	return quake.EventID{}
}

func (s *Service) saveChange(ctx context.Context, tx ports.QuakeTx, ch quake.Change, res *Result) error {
	view, err := s.policy.Derive(ch.ID, ch.Members)
	if err != nil {
		return err
	}
	p := view.Primary
	regions, err := tx.RegionsWithin(ctx, p.Latitude, p.Longitude, s.policy.QueryRadiusKm(p.Magnitude, p.DepthKm))
	if err != nil {
		return err
	}
	level, impacts := s.policy.Assess(p.Magnitude, p.DepthKm, p.Tsunami, regions)
	a := quake.Assessment{View: view, Level: level, Impacts: impacts}
	digest := a.Digest()

	prev, exists, err := tx.Event(ctx, ch.ID)
	if err != nil {
		return err
	}
	var msg ports.OutboxMessage
	switch {
	case !exists:
		if err := tx.SaveEvent(ctx, ports.EventRecord{Assessment: a, Revision: 1, Digest: digest}); err != nil {
			return err
		}
		if msg, err = s.enc.Created(a, 1); err != nil {
			return err
		}
		res.Created++
	case prev.Digest != digest:
		rev := prev.Revision + 1
		if err := tx.SaveEvent(ctx, ports.EventRecord{Assessment: a, Revision: rev, Digest: digest}); err != nil {
			return err
		}
		if msg, err = s.enc.Updated(a, rev, prev.Level); err != nil {
			return err
		}
		res.Updated++
	}
	if msg.MsgID != "" {
		if err := tx.Enqueue(ctx, msg); err != nil {
			return err
		}
	}
	// Kejadian sudah ada di penyimpanan, baru laporan anggotanya ditautkan.
	keys := make([]quake.IdentityKey, len(ch.Members))
	for i, m := range ch.Members {
		keys[i] = m.Key
	}
	return tx.SetMembership(ctx, ch.ID, keys)
}

func (s *Service) end(ctx context.Context, tx ports.QuakeTx, id, mergedInto quake.EventID, res *Result) error {
	prev, exists, err := tx.Event(ctx, id)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("kejadian %s yang dikosongkan tidak ada di penyimpanan", id)
	}
	status, reason := ports.StatusMerged, ports.ExpiryMerged
	if mergedInto.IsZero() {
		status, reason = ports.StatusRetracted, ports.ExpiryRetracted
	}
	at := s.now().UTC()
	rev := prev.Revision + 1
	if err := tx.EndEvent(ctx, id, status, mergedInto, at, rev); err != nil {
		return err
	}
	msg, err := s.enc.Expired(id, at, reason, mergedInto, rev)
	if err != nil {
		return err
	}
	res.Ended++
	return tx.Enqueue(ctx, msg)
}

// Expire mengakhiri kejadian gempa aktif yang masa aktifnya habis pada now.
// Mengembalikan jumlah kejadian yang diakhiri.
func (s *Service) Expire(ctx context.Context, now time.Time, limit int) (int, error) {
	n := 0
	err := s.store.InTx(ctx, func(tx ports.QuakeTx) error {
		n = 0
		if err := tx.LockQuakes(ctx); err != nil {
			return err
		}
		due, err := tx.DueForExpiry(ctx, now, limit)
		if err != nil {
			return err
		}
		for _, ev := range due {
			rev := ev.Revision + 1
			if err := tx.EndEvent(ctx, ev.ID, ports.StatusExpired, quake.EventID{}, now.UTC(), rev); err != nil {
				return err
			}
			msg, err := s.enc.Expired(ev.ID, now.UTC(), ports.ExpiryElapsed, quake.EventID{}, rev)
			if err != nil {
				return err
			}
			if err := tx.Enqueue(ctx, msg); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("mengakhiri kejadian kedaluwarsa: %w", err)
	}
	return n, nil
}

func toClusters(raw map[quake.EventID][]quake.Stored) ([]quake.Cluster, error) {
	out := make([]quake.Cluster, 0, len(raw))
	for id, reports := range raw {
		byKey := map[quake.IdentityKey][]quake.Stored{}
		for _, r := range reports {
			byKey[r.Identity()] = append(byKey[r.Identity()], r)
		}
		c := quake.Cluster{ID: id}
		for _, rs := range byKey {
			sol, err := quake.Merge(rs)
			if err != nil {
				return nil, err
			}
			c.Members = append(c.Members, sol)
		}
		out = append(out, c)
	}
	return out, nil
}

// RunExpiry menjalankan Expire setiap interval sampai ctx selesai. after
// dipanggil setelah setiap putaran (misal untuk membangunkan relay outbox
// atau mencatat galat).
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
