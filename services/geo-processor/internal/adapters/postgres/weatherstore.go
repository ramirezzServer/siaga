package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres/weathersql"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/weather"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// WeatherStore adalah ports.WeatherStore di atas pool pgx.
type WeatherStore struct {
	pool *pgxpool.Pool
}

var _ ports.WeatherStore = (*WeatherStore)(nil)

// NewWeatherStore membungkus pool yang sudah terbuka. Pemanggil yang menutup pool.
func NewWeatherStore(pool *pgxpool.Pool) *WeatherStore { return &WeatherStore{pool: pool} }

// InTx menjalankan fn dalam satu transaksi.
func (s *WeatherStore) InTx(ctx context.Context, fn func(ports.WeatherTx) error) (err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("memulai transaksi: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, rollback(ctx, tx))
		}
	}()
	if err = fn(&weatherTx{tx: tx}); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

type weatherTx struct {
	tx pgx.Tx
}

func (t *weatherTx) LockWeather(ctx context.Context) error {
	if _, err := t.tx.Exec(ctx, weathersql.Lock, weathersql.LockKey); err != nil {
		return fmt.Errorf("advisory lock cuaca: %w", err)
	}
	return nil
}

type textJSON struct {
	Language    string `json:"language"`
	Event       string `json:"event"`
	Headline    string `json:"headline"`
	Description string `json:"description"`
	Instruction string `json:"instruction"`
	SenderName  string `json:"sender_name"`
}

func scanMessage(row pgx.CollectableRow) (weather.Message, error) {
	var (
		m               weather.Message
		source, msgType string
		severity        string
		onset           pgtype.Timestamptz
		texts           []textJSON
		digest          []byte
		event           pgtype.UUID
	)
	err := row.Scan(&source, &m.Identifier, &m.Sender, &m.Sent, &msgType, &m.Category, &m.EventCode,
		&m.Urgency, &severity, &m.Certainty, &m.Effective, &onset, &m.Expires, &texts, &m.Web, &m.Contact,
		&m.SourceURL, &m.AreaDesc, &m.HasArea, &m.FetchedAt, &m.FirstSeenAt, &m.ArchiveKey, &digest, &event)
	if err != nil {
		return m, err
	}
	if len(digest) != len(m.Digest) {
		return m, fmt.Errorf("digest pesan %s berukuran %d", m.Identifier, len(digest))
	}
	copy(m.Digest[:], digest)
	m.Source, m.MsgType, m.Severity = weather.Source(source), weather.MsgType(msgType), weather.Severity(severity)
	for _, p := range []*time.Time{&m.Sent, &m.Effective, &m.Expires, &m.FetchedAt, &m.FirstSeenAt} {
		*p = p.UTC()
	}
	if onset.Valid {
		m.Onset = onset.Time.UTC()
	}
	for _, t := range texts {
		m.Texts = append(m.Texts, weather.Text(t))
	}
	if event.Valid {
		m.Event = event.Bytes
	}
	return m, nil
}

func (t *weatherTx) Message(ctx context.Context, key weather.Key) (*weather.Message, error) {
	rows, err := t.tx.Query(ctx, weathersql.Message, string(key.Source), key.Identifier)
	if err != nil {
		return nil, fmt.Errorf("membaca pesan %s: %w", key, err)
	}
	m, err := pgx.CollectExactlyOneRow(rows, scanMessage)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("membaca pesan %s: %w", key, err)
	}
	if err := t.loadReferences(ctx, key.Source, []*weather.Message{&m}); err != nil {
		return nil, err
	}
	return &m, nil
}

// wkt menulis cincin CAP (lat,lon) sebagai POLYGON WKT (lon lat).
func wkt(r weather.Ring) string {
	var b strings.Builder
	b.WriteString("POLYGON((")
	for i, p := range r {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(p.Lon, 'f', -1, 64))
		b.WriteByte(' ')
		b.WriteString(strconv.FormatFloat(p.Lat, 'f', -1, 64))
	}
	b.WriteString("))")
	return b.String()
}

// invalid menerjemahkan pelanggaran constraint menjadi weather.ErrInvalid:
// pesan yang sama tidak akan pernah lolos bila diulang.
func invalid(err error, what string) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && (pgErr.Code == "23514" || pgErr.Code == "23502") {
		return fmt.Errorf("%w: %s melanggar constraint %s: %w", weather.ErrInvalid, what, pgErr.ConstraintName, err)
	}
	return fmt.Errorf("%s: %w", what, err)
}

func (t *weatherTx) SaveMessage(ctx context.Context, m weather.Message) error {
	texts := make([]textJSON, len(m.Texts))
	for i, tx := range m.Texts {
		texts[i] = textJSON(tx)
	}
	textsJSON, err := json.Marshal(texts)
	if err != nil {
		return fmt.Errorf("serialisasi teks %s: %w", m.Key, err)
	}
	polygons := make([]string, len(m.Polygons))
	for i, r := range m.Polygons {
		polygons[i] = wkt(r)
	}
	var onset pgtype.Timestamptz
	if !m.Onset.IsZero() {
		onset = pgtype.Timestamptz{Time: m.Onset, Valid: true}
	}
	if _, err := t.tx.Exec(ctx, weathersql.SaveMessage,
		string(m.Source), m.Identifier, m.Sender, m.Sent, string(m.MsgType), m.Category, m.EventCode,
		m.Urgency, string(m.Severity), m.Certainty, m.Effective, onset, m.Expires, textsJSON, m.Web,
		m.Contact, m.SourceURL, m.AreaDesc, polygons, m.FetchedAt, m.FirstSeenAt, m.ArchiveKey, m.Digest[:],
	); err != nil {
		return invalid(err, "menyimpan pesan "+m.String())
	}
	if _, err := t.tx.Exec(ctx, weathersql.ClearReferences, string(m.Source), m.Identifier); err != nil {
		return fmt.Errorf("menghapus rujukan %s: %w", m.Key, err)
	}
	if len(m.References) == 0 {
		return nil
	}
	ids := make([]string, len(m.References))
	senders := make([]string, len(m.References))
	sent := make([]time.Time, len(m.References))
	for i, r := range m.References {
		ids[i], senders[i], sent[i] = r.Identifier, r.Sender, r.Sent
	}
	if _, err := t.tx.Exec(ctx, weathersql.SaveReferences, string(m.Source), m.Identifier, ids, senders, sent); err != nil {
		return invalid(err, "menyimpan rujukan "+m.String())
	}
	return nil
}

func (t *weatherTx) Component(ctx context.Context, key weather.Key) ([]weather.Message, error) {
	rows, err := t.tx.Query(ctx, weathersql.Component, string(key.Source), key.Identifier)
	if err != nil {
		return nil, fmt.Errorf("membaca rantai %s: %w", key, err)
	}
	msgs, err := pgx.CollectRows(rows, scanMessage)
	if err != nil {
		return nil, fmt.Errorf("membaca rantai %s: %w", key, err)
	}
	ptrs := make([]*weather.Message, len(msgs))
	for i := range msgs {
		ptrs[i] = &msgs[i]
	}
	if err := t.loadReferences(ctx, key.Source, ptrs); err != nil {
		return nil, err
	}
	return msgs, nil
}

func (t *weatherTx) loadReferences(ctx context.Context, source weather.Source, msgs []*weather.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	byID := make(map[string]*weather.Message, len(msgs))
	ids := make([]string, len(msgs))
	for i, m := range msgs {
		byID[m.Identifier], ids[i] = m, m.Identifier
	}
	rows, err := t.tx.Query(ctx, weathersql.References, string(source), ids)
	if err != nil {
		return fmt.Errorf("membaca rujukan: %w", err)
	}
	type ref struct {
		owner string
		weather.Reference
	}
	refs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ref, error) {
		var x ref
		err := r.Scan(&x.owner, &x.Identifier, &x.Sender, &x.Sent)
		x.Source, x.Sent = source, x.Sent.UTC()
		return x, err
	})
	if err != nil {
		return fmt.Errorf("membaca rujukan: %w", err)
	}
	for _, r := range refs {
		m := byID[r.owner]
		m.References = append(m.References, r.Reference)
	}
	return nil
}

func (t *weatherTx) Footprint(ctx context.Context, key weather.Key) (weather.Footprint, error) {
	var fp weather.Footprint
	err := t.tx.QueryRow(ctx, weathersql.Area, string(key.Source), key.Identifier).
		Scan(&fp.AreaGeoJSON, &fp.Latitude, &fp.Longitude, &fp.AreaKm2)
	if err != nil {
		return fp, fmt.Errorf("membaca area %s: %w", key, err)
	}
	rows, err := t.tx.Query(ctx, weathersql.Coverage, string(key.Source), key.Identifier)
	if err != nil {
		return fp, fmt.Errorf("mencari wilayah terdampak %s: %w", key, err)
	}
	fp.Regions, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (weather.RegionCoverage, error) {
		var rc weather.RegionCoverage
		err := r.Scan(&rc.Code, &rc.Name, &rc.Coverage)
		return rc, err
	})
	if err != nil {
		return fp, fmt.Errorf("mencari wilayah terdampak %s: %w", key, err)
	}
	return fp, nil
}

func (t *weatherTx) Event(ctx context.Context, id hazard.EventID) (ports.StoredEvent, bool, error) {
	ev, err := scanEvent(id, t.tx.QueryRow(ctx, weathersql.Event, pgtype.UUID{Bytes: id, Valid: true}))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ev, false, nil
	case err != nil:
		return ev, false, fmt.Errorf("membaca kejadian %s: %w", id, err)
	}
	return ev, true, nil
}

func (t *weatherTx) SaveEvent(ctx context.Context, rec ports.WeatherEventRecord) error {
	a := rec.Assessment
	c := a.Content
	id := pgtype.UUID{Bytes: a.ID, Valid: true}
	if _, err := t.tx.Exec(ctx, weathersql.SaveEvent,
		id, int(a.Level), a.Root.Identifier, a.OccurredAt, a.DetectedAt, a.ExpiresAt,
		a.Latitude, a.Longitude, c.Identifier, a.Title, a.Summary, rec.Revision, rec.Digest[:],
	); err != nil {
		return invalid(err, "menyimpan kejadian "+a.ID.String())
	}
	tid, _ := c.Text(weather.LanguagePrimary)
	ten, _ := c.Text("en")
	var onset pgtype.Timestamptz
	if !c.Onset.IsZero() {
		onset = pgtype.Timestamptz{Time: c.Onset, Valid: true}
	}
	if _, err := t.tx.Exec(ctx, weathersql.SaveWeather,
		id, a.Current.Identifier, string(a.Current.MsgType), string(c.Severity), c.Urgency, c.Certainty, c.EventCode,
		tid.Event, tid.Headline, tid.Description, tid.Instruction, ten.Event, ten.Headline, ten.Description, ten.Instruction,
		c.AreaDesc, c.Web, c.SourceURL, c.Sent, c.Effective, onset, a.AreaKm2, a.Messages,
	); err != nil {
		return invalid(err, "menyimpan detail cuaca "+a.ID.String())
	}
	if _, err := t.tx.Exec(ctx, weathersql.ClearImpacts, id); err != nil {
		return fmt.Errorf("menghapus wilayah terdampak %s: %w", a.ID, err)
	}
	if len(a.Impacts) == 0 {
		return nil
	}
	codes := make([]string, len(a.Impacts))
	levels := make([]int, len(a.Impacts))
	cov := make([]float64, len(a.Impacts))
	for i, im := range a.Impacts {
		codes[i], levels[i], cov[i] = im.Code, int(im.Level), im.Coverage
	}
	if _, err := t.tx.Exec(ctx, weathersql.SaveImpacts, id, codes, levels, cov); err != nil {
		return invalid(err, "menyimpan wilayah terdampak "+a.ID.String())
	}
	return nil
}

func (t *weatherTx) Reactivate(ctx context.Context, id hazard.EventID) error {
	tag, err := t.tx.Exec(ctx, weathersql.Reactivate, pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		return fmt.Errorf("mengaktifkan lagi %s: %w", id, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("mengaktifkan lagi %s: tidak ditemukan", id)
	}
	return nil
}

func (t *weatherTx) EndEvent(ctx context.Context, id hazard.EventID, status ports.EventStatus, mergedInto hazard.EventID, at time.Time, revision int) error {
	merged := pgtype.UUID{Bytes: mergedInto, Valid: !mergedInto.IsZero()}
	tag, err := t.tx.Exec(ctx, weathersql.EndEvent, pgtype.UUID{Bytes: id, Valid: true}, string(status), merged, at, revision)
	if err != nil {
		return fmt.Errorf("mengakhiri kejadian %s: %w", id, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("mengakhiri kejadian %s: tidak ditemukan", id)
	}
	return nil
}

func (t *weatherTx) SetMembership(ctx context.Context, id hazard.EventID, keys []weather.Key) error {
	if len(keys) == 0 {
		return nil
	}
	ids := make([]string, len(keys))
	for i, k := range keys {
		if k.Source != keys[0].Source {
			return fmt.Errorf("rantai %s berisi pesan dari beberapa penerbit", id)
		}
		ids[i] = k.Identifier
	}
	if _, err := t.tx.Exec(ctx, weathersql.SetMembership, pgtype.UUID{Bytes: id, Valid: true}, string(keys[0].Source), ids); err != nil {
		return fmt.Errorf("menautkan pesan ke %s: %w", id, err)
	}
	return nil
}

func (t *weatherTx) DueForExpiry(ctx context.Context, now time.Time, limit int) ([]ports.StoredEvent, error) {
	rows, err := t.tx.Query(ctx, weathersql.DueForExpiry, now, limit, "weather")
	if err != nil {
		return nil, fmt.Errorf("mencari peringatan kedaluwarsa: %w", err)
	}
	out, err := pgx.CollectRows(rows, scanDueEvent)
	if err != nil {
		return nil, fmt.Errorf("mencari peringatan kedaluwarsa: %w", err)
	}
	return out, nil
}

func (t *weatherTx) Enqueue(ctx context.Context, msg ports.OutboxMessage) error {
	if _, err := t.tx.Exec(ctx, weathersql.Enqueue, msg.Subject, msg.MsgID, msg.Payload); err != nil {
		return fmt.Errorf("menulis outbox %s: %w", msg.MsgID, err)
	}
	return nil
}
