package postgres

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres/floodsql"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/flood"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// FloodStore adalah ports.FloodStore di atas pool pgx.
type FloodStore struct {
	pool *pgxpool.Pool
}

var _ ports.FloodStore = (*FloodStore)(nil)

// NewFloodStore membungkus pool yang sudah terbuka. Pemanggil yang menutup pool.
func NewFloodStore(pool *pgxpool.Pool) *FloodStore { return &FloodStore{pool: pool} }

// InTx menjalankan fn dalam satu transaksi.
func (s *FloodStore) InTx(ctx context.Context, fn func(ports.FloodTx) error) (err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("memulai transaksi: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, rollback(ctx, tx))
		}
	}()
	if err = fn(&floodTx{tx: tx}); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

type floodTx struct {
	tx pgx.Tx
}

// floodInvalid menerjemahkan pelanggaran constraint menjadi flood.ErrInvalid:
// keluaran yang sama tidak akan pernah lolos bila diulang.
func floodInvalid(err error, what string) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && (pgErr.Code == "23514" || pgErr.Code == "23502") {
		return fmt.Errorf("%w: %s melanggar constraint %s: %w", flood.ErrInvalid, what, pgErr.ConstraintName, err)
	}
	return fmt.Errorf("%s: %w", what, err)
}

func uuid(id hazard.EventID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: !id.IsZero()} }

func (t *floodTx) LockFlood(ctx context.Context) error {
	if _, err := t.tx.Exec(ctx, floodsql.Lock, floodsql.LockKey); err != nil {
		return fmt.Errorf("advisory lock banjir: %w", err)
	}
	return nil
}

func (t *floodTx) Site(ctx context.Context, siteID string) (ports.FloodSite, bool, error) {
	var st ports.FloodSite
	var event pgtype.UUID
	err := t.tx.QueryRow(ctx, floodsql.Site, siteID).Scan(&st.LastFetchedAt, &event)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return st, false, nil
	case err != nil:
		return st, false, fmt.Errorf("membaca titik banjir %s: %w", siteID, err)
	}
	st.LastFetchedAt = st.LastFetchedAt.UTC()
	if event.Valid {
		st.EventID = event.Bytes
	}
	return st, true, nil
}

func (t *floodTx) SaveSite(ctx context.Context, siteID string, fetchedAt time.Time, event hazard.EventID) error {
	if _, err := t.tx.Exec(ctx, floodsql.SaveSite, siteID, fetchedAt, uuid(event)); err != nil {
		return floodInvalid(err, "menyimpan titik banjir "+siteID)
	}
	return nil
}

func (t *floodTx) Episode(ctx context.Context, id hazard.EventID) (ports.FloodEpisode, bool, error) {
	ep := ports.FloodEpisode{StoredEvent: ports.StoredEvent{ID: id}}
	var (
		status string
		level  int16
		rev    int32
		digest []byte
	)
	err := t.tx.QueryRow(ctx, floodsql.Episode, uuid(id)).Scan(&status, &level, &rev, &digest, &ep.OpenedAt, &ep.LastExceededAt, &ep.ExpiresAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ep, false, nil
	case err != nil:
		return ep, false, fmt.Errorf("membaca kejadian banjir %s: %w", id, err)
	}
	if len(digest) != len(ep.Digest) {
		return ep, false, fmt.Errorf("digest kejadian %s berukuran %d", id, len(digest))
	}
	ep.Status, ep.Level, ep.Revision = ports.EventStatus(status), hazard.Level(level), int(rev)
	copy(ep.Digest[:], digest)
	ep.OpenedAt, ep.LastExceededAt, ep.ExpiresAt = ep.OpenedAt.UTC(), ep.LastExceededAt.UTC(), ep.ExpiresAt.UTC()
	return ep, true, nil
}

// thresholds meratakan ambang efektif: 4 nilai debit atau 12 nilai indeks
// hujan (jendela 3, 6, 24 jam berurutan), sama dengan constraint
// flood_thresholds_shape.
func thresholds(a flood.Assessment) []float64 {
	if a.Indicator == flood.Discharge {
		return append([]float64(nil), a.DischargeThresholds[:]...)
	}
	var out []float64
	for _, s := range a.RainfallThresholds {
		out = append(out, s[:]...)
	}
	return out
}

func (t *floodTx) SaveEvent(ctx context.Context, rec ports.FloodEventRecord) error {
	a := rec.Assessment
	id := uuid(a.ID)
	if _, err := t.tx.Exec(ctx, floodsql.SaveEvent,
		id, int(a.Level), a.SiteID, a.OpenedAt, a.ExpiresAt, a.Location.Lat, a.Location.Lon,
		a.Title, a.Summary, rec.Revision, rec.Digest[:],
	); err != nil {
		return floodInvalid(err, "menyimpan kejadian banjir "+a.ID.String())
	}
	var peak pgtype.Timestamptz
	if !a.Peak.IsZero() {
		peak = pgtype.Timestamptz{Time: a.Peak, Valid: true}
	}
	if _, err := t.tx.Exec(ctx, floodsql.SaveFlood,
		id, string(a.Indicator), a.SiteID, a.SiteName, a.River, a.Model, a.FetchedAt, int(a.Outlook.Level),
		a.Possible, peak, int(a.MaxLevel), a.OpenedAt, a.LastExceededAt, thresholds(a),
	); err != nil {
		return floodInvalid(err, "menyimpan detail banjir "+a.ID.String())
	}
	if _, err := t.tx.Exec(ctx, floodsql.ClearDays, id); err != nil {
		return fmt.Errorf("menghapus penilaian harian %s: %w", a.ID, err)
	}
	n := len(a.Days)
	dates := make([]time.Time, n)
	levels, windows := make([]int, n), make([]int, n)
	possible, ensemble := make([]bool, n), make([]bool, n)
	q, p75, qmax, r3, r6, r24 := make([]*float64, n), make([]*float64, n), make([]*float64, n), make([]*float64, n), make([]*float64, n), make([]*float64, n)
	for i, d := range a.Days {
		dates[i], levels[i], windows[i], possible[i], ensemble[i] = d.Date, int(d.Level), d.Window, d.Possible, d.FromEnsemble
		q[i], p75[i], qmax[i], r3[i], r6[i], r24[i] = d.Discharge, d.P75, d.Max, d.Rain[0], d.Rain[1], d.Rain[2]
	}
	if _, err := t.tx.Exec(ctx, floodsql.SaveDays, id, dates, levels, possible, q, p75, qmax, ensemble, r3, r6, r24, windows); err != nil {
		return floodInvalid(err, "menyimpan penilaian harian "+a.ID.String())
	}
	return nil
}

func (t *floodTx) EndEvent(ctx context.Context, id hazard.EventID, status ports.EventStatus, mergedInto hazard.EventID, at time.Time, revision int) error {
	tag, err := t.tx.Exec(ctx, floodsql.EndEvent, uuid(id), string(status), uuid(mergedInto), at, revision)
	if err != nil {
		return fmt.Errorf("mengakhiri kejadian %s: %w", id, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("mengakhiri kejadian %s: tidak ditemukan", id)
	}
	return nil
}

func (t *floodTx) DueForExpiry(ctx context.Context, now time.Time, limit int) ([]ports.StoredEvent, error) {
	rows, err := t.tx.Query(ctx, floodsql.DueForExpiry, now, limit, "flood")
	if err != nil {
		return nil, fmt.Errorf("mencari kejadian banjir kedaluwarsa: %w", err)
	}
	out, err := pgx.CollectRows(rows, scanDueEvent)
	if err != nil {
		return nil, fmt.Errorf("mencari kejadian banjir kedaluwarsa: %w", err)
	}
	return out, nil
}

func (t *floodTx) Enqueue(ctx context.Context, msg ports.OutboxMessage) error {
	if _, err := t.tx.Exec(ctx, floodsql.Enqueue, msg.Subject, msg.MsgID, msg.Payload, traceParent(ctx, msg)); err != nil {
		return fmt.Errorf("menulis outbox %s: %w", msg.MsgID, err)
	}
	return nil
}

// SyncThresholds menyelaraskan ref.discharge_threshold dan
// ref.rainfall_threshold dengan kalibrasi dalam satu transaksi.
func (s *FloodStore) SyncThresholds(ctx context.Context, cal flood.Calibration) (res ports.ThresholdSync, err error) {
	if err := cal.Validate(); err != nil {
		return res, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, fmt.Errorf("memulai transaksi: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, rollback(ctx, tx))
		}
	}()
	if _, err = tx.Exec(ctx, floodsql.Lock, floodsql.LockThresholds); err != nil {
		return res, fmt.Errorf("advisory lock ambang: %w", err)
	}
	if err = syncDischarge(ctx, tx, cal, &res); err != nil {
		return res, err
	}
	if err = syncRainfall(ctx, tx, cal, &res); err != nil {
		return res, err
	}
	if err = tx.Commit(ctx); err != nil {
		return res, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}

// count menjumlahkan baris RETURNING (xmax = 0): true = disisipkan.
func count(rows pgx.Rows, total int, res *ports.ThresholdSync) error {
	flags, err := pgx.CollectRows(rows, pgx.RowTo[bool])
	if err != nil {
		return err
	}
	for _, inserted := range flags {
		if inserted {
			res.Inserted++
		} else {
			res.Updated++
		}
	}
	res.Unchanged += total - len(flags)
	return nil
}

func syncDischarge(ctx context.Context, tx pgx.Tx, cal flood.Calibration, res *ports.ThresholdSync) error {
	ids := slices.Sorted(maps.Keys(cal.Discharge))
	n := len(ids)
	rivers, names := make([]string, n), make([]string, n)
	lat, lon, p50, p80, p90, p98, p995, ratio, corr := make([]float64, n), make([]float64, n), make([]float64, n), make([]float64, n), make([]float64, n), make([]float64, n), make([]float64, n), make([]float64, n), make([]float64, n)
	maxl := make([]int, n)
	for i, id := range ids {
		t := cal.Discharge[id]
		rivers[i], names[i], lat[i], lon[i], p50[i] = t.River, t.Name, t.Cell.Lat, t.Cell.Lon, t.P50
		p80[i], p90[i], p98[i], p995[i] = t.Climatology[0], t.Climatology[1], t.Climatology[2], t.Climatology[3]
		ratio[i], corr[i], maxl[i] = t.SeamlessRatio, t.Correction, int(t.MaxLevel)
	}
	src := cal.DischargeSource
	rows, err := tx.Query(ctx, floodsql.UpsertDischarge, ids, rivers, names, lat, lon, p50, p80, p90, p98, p995, ratio, corr, maxl,
		src.Period.From, src.Period.To, src.SHA256[:])
	if err != nil {
		return floodInvalid(err, "menyimpan ambang debit")
	}
	if err := count(rows, n, res); err != nil {
		return floodInvalid(err, "menyimpan ambang debit")
	}
	tag, err := tx.Exec(ctx, floodsql.DeleteDischarge, ids)
	if err != nil {
		return fmt.Errorf("menghapus ambang debit lama: %w", err)
	}
	res.Deleted += int(tag.RowsAffected())
	return nil
}

func syncRainfall(ctx context.Context, tx pgx.Tx, cal flood.Calibration, res *ports.ThresholdSync) error {
	var (
		ids, names               []string
		windows, maxl            []int
		p50, p80, p90, p98, p995 []float64
	)
	for _, id := range slices.Sorted(maps.Keys(cal.Rainfall)) {
		t := cal.Rainfall[id]
		for w, hours := range flood.Windows {
			ids, names = append(ids, id), append(names, t.Name)
			windows, maxl = append(windows, hours), append(maxl, int(t.MaxLevel))
			s := t.Windows[w]
			p50, p80, p90, p98, p995 = append(p50, t.P50[w]), append(p80, s[0]), append(p90, s[1]), append(p98, s[2]), append(p995, s[3])
		}
	}
	src := cal.RainfallSource
	rows, err := tx.Query(ctx, floodsql.UpsertRainfall, ids, windows, names, p50, p80, p90, p98, p995, maxl,
		src.Period.From, src.Period.To, src.SHA256[:])
	if err != nil {
		return floodInvalid(err, "menyimpan ambang indeks hujan")
	}
	if err := count(rows, len(ids), res); err != nil {
		return floodInvalid(err, "menyimpan ambang indeks hujan")
	}
	tag, err := tx.Exec(ctx, floodsql.DeleteRainfall, ids, windows)
	if err != nil {
		return fmt.Errorf("menghapus ambang indeks hujan lama: %w", err)
	}
	res.Deleted += int(tag.RowsAffected())
	return nil
}
