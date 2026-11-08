package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres/firesql"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hotspot"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// FireStore adalah ports.FireStore di atas pool pgx.
type FireStore struct {
	pool *pgxpool.Pool
}

var _ ports.FireStore = (*FireStore)(nil)

// NewFireStore membungkus pool yang sudah terbuka. Pemanggil yang menutup pool.
func NewFireStore(pool *pgxpool.Pool) *FireStore { return &FireStore{pool: pool} }

// InTx menjalankan fn dalam satu transaksi.
func (s *FireStore) InTx(ctx context.Context, fn func(ports.FireTx) error) (err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("memulai transaksi: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, rollback(ctx, tx))
		}
	}()
	if err = fn(&fireTx{tx: tx}); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

type fireTx struct {
	tx pgx.Tx
}

// fireInvalid menerjemahkan pelanggaran constraint menjadi fire.ErrInvalid:
// deteksi yang sama tidak akan pernah lolos bila diulang.
func fireInvalid(err error, what string) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && (pgErr.Code == "23514" || pgErr.Code == "23502") {
		return fmt.Errorf("%w: %s melanggar constraint %s: %w", fire.ErrInvalid, what, pgErr.ConstraintName, err)
	}
	return fmt.Errorf("%s: %w", what, err)
}

func (t *fireTx) LockFire(ctx context.Context) error {
	if _, err := t.tx.Exec(ctx, firesql.Lock, firesql.LockKey); err != nil {
		return fmt.Errorf("advisory lock titik api: %w", err)
	}
	return nil
}

func (t *fireTx) HasDetection(ctx context.Context, id string) (bool, error) {
	var ok bool
	if err := t.tx.QueryRow(ctx, firesql.HasDetection, id).Scan(&ok); err != nil {
		return false, fmt.Errorf("memeriksa deteksi %s: %w", id, err)
	}
	return ok, nil
}

func scanMember(row pgx.CollectableRow) (ports.FireMember, error) {
	var (
		m    ports.FireMember
		id   pgtype.UUID
		conf string
	)
	if err := row.Scan(&id, &m.ID, &m.Product, &m.Lat, &m.Lon, &m.DetectedAt, &conf, &m.FRPMW, &m.FetchedAt); err != nil {
		return m, err
	}
	m.EventID = id.Bytes
	m.Confidence = hotspot.Confidence(conf)
	m.DetectedAt, m.FetchedAt = m.DetectedAt.UTC(), m.FetchedAt.UTC()
	return m, nil
}

func (t *fireTx) Nearby(ctx context.Context, d fire.Detection, gap time.Duration, km float64) ([]ports.FireMember, error) {
	rows, err := t.tx.Query(ctx, firesql.Nearby, d.Lat, d.Lon, d.DetectedAt, pgtype.Interval{Microseconds: gap.Microseconds(), Valid: true}, km*1000)
	if err != nil {
		return nil, fmt.Errorf("mencari kejadian titik api di sekitar %s: %w", d.ID, err)
	}
	out, err := pgx.CollectRows(rows, scanMember)
	if err != nil {
		return nil, fmt.Errorf("mencari kejadian titik api di sekitar %s: %w", d.ID, err)
	}
	return out, nil
}

func (t *fireTx) Episode(ctx context.Context, id hazard.EventID) (ports.FireEpisode, bool, error) {
	ep := ports.FireEpisode{StoredEvent: ports.StoredEvent{ID: id}}
	var (
		status string
		level  int16
		rev    int32
		n      int32
		digest []byte
	)
	err := t.tx.QueryRow(ctx, firesql.Episode, uuid(id)).Scan(&status, &level, &rev, &digest, &ep.OpenedBy, &ep.FirstDetected, &ep.ExpiresAt, &n)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ep, false, nil
	case err != nil:
		return ep, false, fmt.Errorf("membaca kejadian titik api %s: %w", id, err)
	}
	if len(digest) != len(ep.Digest) {
		return ep, false, fmt.Errorf("digest kejadian %s berukuran %d", id, len(digest))
	}
	ep.Status, ep.Level, ep.Revision, ep.DetectionCount = ports.EventStatus(status), hazard.Level(level), int(rev), int(n)
	copy(ep.Digest[:], digest)
	ep.FirstDetected, ep.ExpiresAt = ep.FirstDetected.UTC(), ep.ExpiresAt.UTC()
	return ep, true, nil
}

func (t *fireTx) Members(ctx context.Context, id hazard.EventID) ([]fire.Detection, error) {
	rows, err := t.tx.Query(ctx, firesql.Members, uuid(id))
	if err != nil {
		return nil, fmt.Errorf("membaca anggota kejadian %s: %w", id, err)
	}
	ms, err := pgx.CollectRows(rows, scanMember)
	if err != nil {
		return nil, fmt.Errorf("membaca anggota kejadian %s: %w", id, err)
	}
	out := make([]fire.Detection, len(ms))
	for i, m := range ms {
		out[i] = m.Detection
	}
	return out, nil
}

func (t *fireTx) SaveEvent(ctx context.Context, rec ports.FireEventRecord) error {
	a := rec.Assessment
	id := uuid(a.ID)
	if _, err := t.tx.Exec(ctx, firesql.SaveEvent,
		id, int(a.Level), a.OpenedBy, a.First, a.FirstFetchedAt, a.ExpiresAt, a.Lat, a.Lon,
		a.Title, a.Summary, rec.Revision, rec.Digest[:],
	); err != nil {
		return fireInvalid(err, "menyimpan kejadian titik api "+a.ID.String())
	}
	if _, err := t.tx.Exec(ctx, firesql.SaveFire,
		id, a.OpenedBy, a.Detections, a.Overpasses, a.HighConfidence, a.PeakDetections, a.PeakOverpasses,
		a.First, a.Last, a.ReachedAt, a.FirstFetchedAt, a.LastFetchedAt, a.Products, a.MaxFRPMW, a.SiagaCount, a.WaspadaPasses,
	); err != nil {
		return fireInvalid(err, "menyimpan detail titik api "+a.ID.String())
	}
	return nil
}

func (t *fireTx) AddDetection(ctx context.Context, event hazard.EventID, d fire.Detection) error {
	if _, err := t.tx.Exec(ctx, firesql.AddDetection,
		d.ID, uuid(event), d.Product, d.Lat, d.Lon, d.DetectedAt, string(d.Confidence), d.FRPMW, d.FetchedAt,
	); err != nil {
		return fireInvalid(err, "menyimpan deteksi "+d.ID)
	}
	return nil
}

func (t *fireTx) MoveDetections(ctx context.Context, from, to hazard.EventID) error {
	if _, err := t.tx.Exec(ctx, firesql.MoveDetections, uuid(from), uuid(to)); err != nil {
		return fmt.Errorf("memindahkan anggota %s ke %s: %w", from, to, err)
	}
	return nil
}

func (t *fireTx) EndEvent(ctx context.Context, id hazard.EventID, status ports.EventStatus, mergedInto hazard.EventID, at time.Time, revision int) error {
	tag, err := t.tx.Exec(ctx, firesql.EndEvent, uuid(id), string(status), uuid(mergedInto), at, revision)
	if err != nil {
		return fmt.Errorf("mengakhiri kejadian %s: %w", id, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("mengakhiri kejadian %s: tidak ditemukan", id)
	}
	return nil
}

func (t *fireTx) DueForExpiry(ctx context.Context, now time.Time, limit int) ([]ports.StoredEvent, error) {
	rows, err := t.tx.Query(ctx, firesql.DueForExpiry, now, limit, "fire")
	if err != nil {
		return nil, fmt.Errorf("mencari kejadian titik api kedaluwarsa: %w", err)
	}
	out, err := pgx.CollectRows(rows, scanDueEvent)
	if err != nil {
		return nil, fmt.Errorf("mencari kejadian titik api kedaluwarsa: %w", err)
	}
	return out, nil
}

func (t *fireTx) Enqueue(ctx context.Context, msg ports.OutboxMessage) error {
	if _, err := t.tx.Exec(ctx, firesql.Enqueue, msg.Subject, msg.MsgID, msg.Payload, traceParent(ctx, msg)); err != nil {
		return fmt.Errorf("menulis outbox %s: %w", msg.MsgID, err)
	}
	return nil
}

// SyncStatic menyelaraskan ref.fire_static_source dengan kalibrasi dalam
// satu transaksi.
func (s *FireStore) SyncStatic(ctx context.Context, cal fire.Calibration) (res ports.ThresholdSync, err error) {
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
	if _, err = tx.Exec(ctx, firesql.Lock, firesql.LockStatic); err != nil {
		return res, fmt.Errorf("advisory lock saringan: %w", err)
	}
	n := len(cal.Static)
	ids, reasons := make([]string, n), make([]string, n)
	lat, lon, radius := make([]float64, n), make([]float64, n), make([]float64, n)
	counts := make([]int32, n)
	first, last := make([]time.Time, n), make([]time.Time, n)
	for i, st := range cal.Static {
		ids[i], reasons[i], lat[i], lon[i], radius[i] = st.ID, string(st.Reason), st.Lat, st.Lon, st.RadiusKm
		counts[i], first[i], last[i] = int32(min(st.Detections, 1<<31-1)), st.First, st.Last //nolint:gosec // dibatasi di atas
	}
	rows, err := tx.Query(ctx, firesql.UpsertStatic, ids, lat, lon, radius, reasons, counts, first, last,
		cal.Period.From, cal.Period.To, cal.SHA256[:])
	if err != nil {
		return res, fireInvalid(err, "menyimpan sumber panas tetap")
	}
	if err = count(rows, n, &res); err != nil {
		return res, fireInvalid(err, "menyimpan sumber panas tetap")
	}
	tag, err := tx.Exec(ctx, firesql.DeleteStatic, ids)
	if err != nil {
		return res, fmt.Errorf("menghapus sumber panas tetap lama: %w", err)
	}
	res.Deleted += int(tag.RowsAffected())
	if err = tx.Commit(ctx); err != nil {
		return res, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}
