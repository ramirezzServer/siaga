package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres/quakesql"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// QuakeStore adalah ports.QuakeStore dan ports.Outbox di atas pool pgx.
type QuakeStore struct {
	pool *pgxpool.Pool
}

var (
	_ ports.QuakeStore = (*QuakeStore)(nil)
	_ ports.Outbox     = (*QuakeStore)(nil)
)

// NewQuakeStore membungkus pool yang sudah terbuka. Pemanggil yang menutup pool.
func NewQuakeStore(pool *pgxpool.Pool) *QuakeStore { return &QuakeStore{pool: pool} }

// InTx menjalankan fn dalam satu transaksi.
func (s *QuakeStore) InTx(ctx context.Context, fn func(ports.QuakeTx) error) (err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("memulai transaksi: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, rollback(ctx, tx))
		}
	}()
	if err = fn(&quakeTx{tx: tx}); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Drain menerbitkan pesan outbox tertua lalu menghapusnya dalam satu transaksi.
func (s *QuakeStore) Drain(ctx context.Context, limit int, publish func(context.Context, ports.OutboxMessage) error) (n int, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("memulai transaksi outbox: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, rollback(ctx, tx))
		}
	}()
	rows, err := tx.Query(ctx, quakesql.OutboxBatch, limit)
	if err != nil {
		return 0, fmt.Errorf("membaca outbox: %w", err)
	}
	type item struct {
		id  int64
		msg ports.OutboxMessage
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (item, error) {
		var it item
		err := r.Scan(&it.id, &it.msg.Subject, &it.msg.MsgID, &it.msg.Payload)
		return it, err
	})
	if err != nil {
		return 0, fmt.Errorf("membaca outbox: %w", err)
	}
	var done []int64
	var pubErr error
	for _, it := range items {
		if pubErr = publish(ctx, it.msg); pubErr != nil {
			break
		}
		done = append(done, it.id)
	}
	if len(done) > 0 {
		if _, err = tx.Exec(ctx, quakesql.OutboxDelete, done); err != nil {
			return 0, fmt.Errorf("menghapus outbox: %w", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit outbox: %w", err)
	}
	return len(done), pubErr
}

type quakeTx struct {
	tx pgx.Tx
}

func (t *quakeTx) LockQuakes(ctx context.Context) error {
	if _, err := t.tx.Exec(ctx, quakesql.Lock, quakesql.LockKey); err != nil {
		return fmt.Errorf("advisory lock gempa: %w", err)
	}
	return nil
}

func scanReport(row pgx.CollectableRow, extra ...any) (quake.Stored, error) {
	var (
		s             quake.Stored
		source, feed  string
		tsunami       string
		sourceUpdated pgtype.Timestamptz
	)
	dest := append(extra, &source, &s.EventID, &feed, &s.OccurredAt, &s.Latitude, &s.Longitude,
		&s.Magnitude, &s.MagnitudeType, &s.DepthKm, &s.Place, &s.Felt, &tsunami, &s.PotentialText,
		&s.ShakemapURL, &sourceUpdated, &s.ReviewStatus, &s.AlternateIDs, &s.SourceURL,
		&s.FetchedAt, &s.FirstSeenAt, &s.ArchiveKey)
	if err := row.Scan(dest...); err != nil {
		return s, err
	}
	s.Source, s.Feed = quake.Source(source), quake.Feed(feed)
	ts, err := quake.ParseTsunami(tsunami)
	if err != nil {
		return s, err
	}
	s.Tsunami = ts
	s.OccurredAt = s.OccurredAt.UTC()
	s.FetchedAt = s.FetchedAt.UTC()
	s.FirstSeenAt = s.FirstSeenAt.UTC()
	if sourceUpdated.Valid {
		s.SourceUpdatedAt = sourceUpdated.Time.UTC()
	}
	if len(s.AlternateIDs) == 0 {
		s.AlternateIDs = nil
	}
	return s, nil
}

func (t *quakeTx) Report(ctx context.Context, key quake.ReportKey) (*quake.Stored, error) {
	rows, err := t.tx.Query(ctx, quakesql.Report, string(key.Source), key.EventID, string(key.Feed))
	if err != nil {
		return nil, fmt.Errorf("membaca laporan %s: %w", key.IdentityKey, err)
	}
	st, err := pgx.CollectExactlyOneRow(rows, func(r pgx.CollectableRow) (quake.Stored, error) { return scanReport(r) })
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("membaca laporan %s: %w", key.IdentityKey, err)
	}
	return &st, nil
}

func (t *quakeTx) SaveReport(ctx context.Context, r quake.Stored) error {
	digest := r.ContentDigest()
	var updated pgtype.Timestamptz
	if !r.SourceUpdatedAt.IsZero() {
		updated = pgtype.Timestamptz{Time: r.SourceUpdatedAt, Valid: true}
	}
	alt := r.AlternateIDs
	if alt == nil {
		alt = []string{}
	}
	_, err := t.tx.Exec(ctx, quakesql.SaveReport,
		string(r.Source), r.EventID, string(r.Feed), r.OccurredAt, r.Latitude, r.Longitude,
		r.Magnitude, r.MagnitudeType, r.DepthKm, r.Place, r.Felt, r.Tsunami.String(),
		r.PotentialText, r.ShakemapURL, updated, r.ReviewStatus, alt, r.SourceURL,
		r.FetchedAt, r.FirstSeenAt, r.ArchiveKey, digest[:])
	if err != nil {
		return fmt.Errorf("menyimpan laporan %s: %w", r.Identity(), err)
	}
	return nil
}

func (t *quakeTx) IdentityReports(ctx context.Context, key quake.IdentityKey) ([]quake.Stored, error) {
	rows, err := t.tx.Query(ctx, quakesql.IdentityReports, string(key.Source), key.EventID)
	if err != nil {
		return nil, fmt.Errorf("membaca laporan %s: %w", key, err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (quake.Stored, error) { return scanReport(r) })
	if err != nil {
		return nil, fmt.Errorf("membaca laporan %s: %w", key, err)
	}
	return out, nil
}

func (t *quakeTx) EventOf(ctx context.Context, key quake.IdentityKey) (quake.EventID, error) {
	var id pgtype.UUID
	err := t.tx.QueryRow(ctx, quakesql.EventOf, string(key.Source), key.EventID).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return quake.EventID{}, nil
	case err != nil:
		return quake.EventID{}, fmt.Errorf("mencari kejadian %s: %w", key, err)
	}
	return quake.EventID(id.Bytes), nil
}

func uuids(ids []quake.EventID) []pgtype.UUID {
	out := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		out[i] = pgtype.UUID{Bytes: id, Valid: true}
	}
	return out
}

func (t *quakeTx) Clusters(ctx context.Context, from, to time.Time, include []quake.EventID) (map[quake.EventID][]quake.Stored, error) {
	rows, err := t.tx.Query(ctx, quakesql.Clusters, from, to, uuids(include))
	if err != nil {
		return nil, fmt.Errorf("membaca kandidat kejadian: %w", err)
	}
	type member struct {
		event quake.EventID
		rep   quake.Stored
	}
	members, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (member, error) {
		var id pgtype.UUID
		st, err := scanReport(r, &id)
		return member{event: quake.EventID(id.Bytes), rep: st}, err
	})
	if err != nil {
		return nil, fmt.Errorf("membaca kandidat kejadian: %w", err)
	}
	out := map[quake.EventID][]quake.Stored{}
	for _, m := range members {
		out[m.event] = append(out[m.event], m.rep)
	}
	return out, nil
}

func (t *quakeTx) EventExists(ctx context.Context, id quake.EventID) (bool, error) {
	var ok bool
	if err := t.tx.QueryRow(ctx, quakesql.EventExists, pgtype.UUID{Bytes: id, Valid: true}).Scan(&ok); err != nil {
		return false, fmt.Errorf("cek kejadian %s: %w", id, err)
	}
	return ok, nil
}

func scanEvent(id quake.EventID, row pgx.Row) (ports.StoredEvent, error) {
	ev := ports.StoredEvent{ID: id}
	var (
		status string
		level  int16
		rev    int32
		digest []byte
	)
	if err := row.Scan(&status, &level, &rev, &digest); err != nil {
		return ev, err
	}
	if len(digest) != len(ev.Digest) {
		return ev, fmt.Errorf("digest kejadian %s berukuran %d", id, len(digest))
	}
	ev.Status, ev.Level, ev.Revision = ports.EventStatus(status), quake.Level(level), int(rev)
	copy(ev.Digest[:], digest)
	return ev, nil
}

func (t *quakeTx) Event(ctx context.Context, id quake.EventID) (ports.StoredEvent, bool, error) {
	ev, err := scanEvent(id, t.tx.QueryRow(ctx, quakesql.Event, pgtype.UUID{Bytes: id, Valid: true}))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ev, false, nil
	case err != nil:
		return ev, false, fmt.Errorf("membaca kejadian %s: %w", id, err)
	}
	return ev, true, nil
}

// degreesFor menghitung radius derajat yang pasti mencakup radiusKm di sekitar
// lintang lat, untuk filter indeks sebelum jarak geodesik.
func degreesFor(lat, radiusKm float64) float64 {
	cos := math.Max(math.Cos(lat*math.Pi/180), 0.01)
	return radiusKm/(110.0*cos)*1.1 + 0.01
}

func (t *quakeTx) RegionsWithin(ctx context.Context, lat, lon, radiusKm float64) ([]quake.RegionDistance, error) {
	if radiusKm <= 0 {
		return nil, nil
	}
	rows, err := t.tx.Query(ctx, quakesql.RegionsWithin, lat, lon, radiusKm*1000, degreesFor(lat, radiusKm))
	if err != nil {
		return nil, fmt.Errorf("mencari wilayah terdampak: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (quake.RegionDistance, error) {
		var rd quake.RegionDistance
		err := r.Scan(&rd.Code, &rd.Name, &rd.DistanceKm)
		return rd, err
	})
	if err != nil {
		return nil, fmt.Errorf("mencari wilayah terdampak: %w", err)
	}
	return out, nil
}

func (t *quakeTx) SetMembership(ctx context.Context, id quake.EventID, members []quake.IdentityKey) error {
	sources := make([]string, len(members))
	ids := make([]string, len(members))
	for i, m := range members {
		sources[i], ids[i] = string(m.Source), m.EventID
	}
	if _, err := t.tx.Exec(ctx, quakesql.SetMembership, pgtype.UUID{Bytes: id, Valid: true}, sources, ids); err != nil {
		return fmt.Errorf("menautkan laporan ke %s: %w", id, err)
	}
	return nil
}

func (t *quakeTx) Detach(ctx context.Context, key quake.IdentityKey) error {
	if _, err := t.tx.Exec(ctx, quakesql.Detach, string(key.Source), key.EventID); err != nil {
		return fmt.Errorf("melepas %s: %w", key, err)
	}
	return nil
}

func (t *quakeTx) SaveEvent(ctx context.Context, rec ports.EventRecord) error {
	a := rec.Assessment
	p := a.Primary
	id := pgtype.UUID{Bytes: a.ID, Valid: true}
	if _, err := t.tx.Exec(ctx, quakesql.SaveEvent,
		id, int(a.Level), string(p.Key.Source), p.Key.EventID, p.OccurredAt, a.DetectedAt, a.ExpiresAt,
		p.Latitude, p.Longitude, a.FeltRadiusKm, a.Title, a.Summary, rec.Revision, rec.Digest[:],
	); err != nil {
		return fmt.Errorf("menyimpan kejadian %s: %w", a.ID, err)
	}
	if _, err := t.tx.Exec(ctx, quakesql.SaveQuake,
		id, p.Magnitude, p.MagnitudeType, p.DepthKm, a.FeltRadiusKm, p.Place, p.Felt,
		p.Tsunami.String(), p.ShakemapURL, p.SourceURL,
	); err != nil {
		return fmt.Errorf("menyimpan detail gempa %s: %w", a.ID, err)
	}
	if _, err := t.tx.Exec(ctx, quakesql.ClearImpacts, id); err != nil {
		return fmt.Errorf("menghapus wilayah terdampak %s: %w", a.ID, err)
	}
	if len(a.Impacts) == 0 {
		return nil
	}
	codes := make([]string, len(a.Impacts))
	km := make([]float64, len(a.Impacts))
	levels := make([]int, len(a.Impacts))
	felt := make([]bool, len(a.Impacts))
	for i, im := range a.Impacts {
		codes[i], km[i], levels[i], felt[i] = im.Code, im.DistanceKm, int(im.Level), im.WithinFelt
	}
	if _, err := t.tx.Exec(ctx, quakesql.SaveImpacts, id, codes, km, levels, felt); err != nil {
		return fmt.Errorf("menyimpan wilayah terdampak %s: %w", a.ID, err)
	}
	return nil
}

func (t *quakeTx) EndEvent(ctx context.Context, id quake.EventID, status ports.EventStatus, mergedInto quake.EventID, at time.Time, revision int) error {
	merged := pgtype.UUID{Bytes: mergedInto, Valid: !mergedInto.IsZero()}
	tag, err := t.tx.Exec(ctx, quakesql.EndEvent, pgtype.UUID{Bytes: id, Valid: true}, string(status), merged, at, revision)
	if err != nil {
		return fmt.Errorf("mengakhiri kejadian %s: %w", id, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("mengakhiri kejadian %s: tidak ditemukan", id)
	}
	return nil
}

func (t *quakeTx) DueForExpiry(ctx context.Context, now time.Time, limit int) ([]ports.StoredEvent, error) {
	rows, err := t.tx.Query(ctx, quakesql.DueForExpiry, now, limit)
	if err != nil {
		return nil, fmt.Errorf("mencari kejadian kedaluwarsa: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ports.StoredEvent, error) {
		var id pgtype.UUID
		var rest struct {
			status string
			level  int16
			rev    int32
			digest []byte
		}
		if err := r.Scan(&id, &rest.status, &rest.level, &rest.rev, &rest.digest); err != nil {
			return ports.StoredEvent{}, err
		}
		ev := ports.StoredEvent{
			ID: quake.EventID(id.Bytes), Status: ports.EventStatus(rest.status),
			Level: quake.Level(rest.level), Revision: int(rest.rev),
		}
		copy(ev.Digest[:], rest.digest)
		return ev, nil
	})
	if err != nil {
		return nil, fmt.Errorf("mencari kejadian kedaluwarsa: %w", err)
	}
	return out, nil
}

func (t *quakeTx) Enqueue(ctx context.Context, msg ports.OutboxMessage) error {
	if _, err := t.tx.Exec(ctx, quakesql.Enqueue, msg.Subject, msg.MsgID, msg.Payload); err != nil {
		return fmt.Errorf("menulis outbox %s: %w", msg.MsgID, err)
	}
	return nil
}
