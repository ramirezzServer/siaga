package warnings

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/weather"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// memStore meniru semantik adapter PostgreSQL di memori: transaksi dengan
// salinan keadaan, pesan tanpa poligon saat dibaca ulang, rantai lewat
// rujukan dua arah, status kejadian, dan outbox.
type memStore struct {
	state memState
	// regions menentukan irisan wilayah dari titik pertama poligon pesan.
	regions func(first weather.Point) []weather.RegionCoverage
	failOn  string
}

type memEvent struct {
	rec        ports.WeatherEventRecord
	status     ports.EventStatus
	mergedInto hazard.EventID
	endedAt    time.Time
}

type memState struct {
	msgs   map[weather.Key]weather.Message
	events map[hazard.EventID]*memEvent
	outbox []ports.OutboxMessage
}

func newMemStore() *memStore {
	return &memStore{
		state: memState{msgs: map[weather.Key]weather.Message{}, events: map[hazard.EventID]*memEvent{}},
		regions: func(p weather.Point) []weather.RegionCoverage {
			return []weather.RegionCoverage{{Code: "32.73.01.1001", Name: "Sukarasa", Coverage: 0.9}, {Code: "32.73.01.1002", Name: "Gegerkalong", Coverage: 0.3}}
		},
	}
}

func (s memState) clone() memState {
	c := memState{msgs: map[weather.Key]weather.Message{}, events: map[hazard.EventID]*memEvent{}, outbox: slices.Clone(s.outbox)}
	for k, v := range s.msgs {
		c.msgs[k] = v
	}
	for k, v := range s.events {
		cp := *v
		c.events[k] = &cp
	}
	return c
}

var errInjected = errors.New("galat buatan")

func (m *memStore) InTx(_ context.Context, fn func(ports.WeatherTx) error) error {
	tx := &memTx{store: m, st: m.state.clone()}
	if err := fn(tx); err != nil {
		return err
	}
	m.state = tx.st
	return nil
}

type memTx struct {
	store *memStore
	st    memState
}

func (t *memTx) fail(op string) error {
	if t.store.failOn == op {
		return fmt.Errorf("%s: %w", op, errInjected)
	}
	return nil
}

func (t *memTx) LockWeather(context.Context) error { return t.fail("lock") }

// stored meniru pesan yang dibaca ulang dari database: poligon tidak dimuat.
func stored(m weather.Message) weather.Message {
	m.Polygons = nil
	return m
}

func (t *memTx) Message(_ context.Context, k weather.Key) (*weather.Message, error) {
	if err := t.fail("message"); err != nil {
		return nil, err
	}
	m, ok := t.st.msgs[k]
	if !ok {
		return nil, nil
	}
	s := stored(m)
	return &s, nil
}

func (t *memTx) SaveMessage(_ context.Context, m weather.Message) error {
	if err := t.fail("save-message"); err != nil {
		return err
	}
	m.Event = t.st.msgs[m.Key].Event
	t.st.msgs[m.Key] = m
	return nil
}

func (t *memTx) Component(_ context.Context, k weather.Key) ([]weather.Message, error) {
	if err := t.fail("component"); err != nil {
		return nil, err
	}
	// Graf tak berarah: pesan ↔ rujukannya, termasuk node yang belum diterima.
	adj := map[weather.Key][]weather.Key{}
	for _, m := range t.st.msgs {
		for _, r := range m.References {
			adj[m.Key] = append(adj[m.Key], r.Key)
			adj[r.Key] = append(adj[r.Key], m.Key)
		}
	}
	seen := map[weather.Key]bool{k: true}
	queue := []weather.Key{k}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, n := range adj[cur] {
			if !seen[n] {
				seen[n] = true
				queue = append(queue, n)
			}
		}
	}
	var out []weather.Message
	for key := range seen {
		if m, ok := t.st.msgs[key]; ok {
			out = append(out, stored(m))
		}
	}
	slices.SortFunc(out, func(a, b weather.Message) int { return cmp.Or(a.Sent.Compare(b.Sent), a.Compare(b.Key)) })
	return out, nil
}

func (t *memTx) Footprint(_ context.Context, k weather.Key) (weather.Footprint, error) {
	if err := t.fail("footprint"); err != nil {
		return weather.Footprint{}, err
	}
	m := t.st.msgs[k]
	first := m.Polygons[0][0]
	return weather.Footprint{
		AreaGeoJSON: fmt.Sprintf("%v", m.Polygons), Latitude: first.Lat, Longitude: first.Lon,
		AreaKm2: float64(len(m.Polygons)) * 25, Regions: t.store.regions(first),
	}, nil
}

func (t *memTx) Event(_ context.Context, id hazard.EventID) (ports.StoredEvent, bool, error) {
	if err := t.fail("event"); err != nil {
		return ports.StoredEvent{}, false, err
	}
	ev, ok := t.st.events[id]
	if !ok {
		return ports.StoredEvent{}, false, nil
	}
	return ports.StoredEvent{ID: id, Status: ev.status, Level: ev.rec.Level, Revision: ev.rec.Revision, Digest: ev.rec.Digest}, true, nil
}

func (t *memTx) SaveEvent(_ context.Context, rec ports.WeatherEventRecord) error {
	if err := t.fail("save-event"); err != nil {
		return err
	}
	if ev, ok := t.st.events[rec.ID]; ok {
		ev.rec = rec
		return nil
	}
	t.st.events[rec.ID] = &memEvent{rec: rec, status: ports.StatusActive}
	return nil
}

func (t *memTx) Reactivate(_ context.Context, id hazard.EventID) error {
	ev, ok := t.st.events[id]
	if !ok {
		return errors.New("tidak ada")
	}
	ev.status, ev.mergedInto, ev.endedAt = ports.StatusActive, hazard.EventID{}, time.Time{}
	return nil
}

func (t *memTx) EndEvent(_ context.Context, id hazard.EventID, status ports.EventStatus, into hazard.EventID, at time.Time, rev int) error {
	if err := t.fail("end"); err != nil {
		return err
	}
	ev, ok := t.st.events[id]
	if !ok {
		return errors.New("tidak ada")
	}
	if status == ports.StatusMerged {
		if _, ok := t.st.events[into]; !ok {
			return errors.New("tujuan gabung tidak ada (FK)")
		}
	}
	ev.status, ev.mergedInto, ev.endedAt, ev.rec.Revision = status, into, at, rev
	return nil
}

func (t *memTx) SetMembership(_ context.Context, id hazard.EventID, keys []weather.Key) error {
	if err := t.fail("membership"); err != nil {
		return err
	}
	if _, ok := t.st.events[id]; !ok {
		return errors.New("kejadian tidak ada (FK)")
	}
	for _, k := range keys {
		m := t.st.msgs[k]
		m.Event = id
		t.st.msgs[k] = m
	}
	return nil
}

func (t *memTx) DueForExpiry(_ context.Context, now time.Time, limit int) ([]ports.StoredEvent, error) {
	if err := t.fail("due"); err != nil {
		return nil, err
	}
	var out []ports.StoredEvent
	for id, ev := range t.st.events {
		if ev.status == ports.StatusActive && !ev.rec.ExpiresAt.After(now) {
			out = append(out, ports.StoredEvent{ID: id, Status: ev.status, Level: ev.rec.Level, Revision: ev.rec.Revision, Digest: ev.rec.Digest})
		}
	}
	slices.SortFunc(out, func(a, b ports.StoredEvent) int { return a.ID.Compare(b.ID) })
	return out[:min(len(out), limit)], nil
}

func (t *memTx) Enqueue(_ context.Context, msg ports.OutboxMessage) error {
	if err := t.fail("enqueue"); err != nil {
		return err
	}
	if slices.ContainsFunc(t.st.outbox, func(o ports.OutboxMessage) bool { return o.MsgID == msg.MsgID }) {
		return nil
	}
	t.st.outbox = append(t.st.outbox, msg)
	return nil
}

// fakeEncoder menulis subjek dan ID pesan yang bisa diperiksa test.
type fakeEncoder struct{ fail bool }

func (e fakeEncoder) msg(kind string, id hazard.EventID, rev int, extra string) (ports.OutboxMessage, error) {
	if e.fail {
		return ports.OutboxMessage{}, errInjected
	}
	return ports.OutboxMessage{Subject: "hazard.weather." + kind, MsgID: fmt.Sprintf("%s:r%d", id, rev), Payload: []byte(extra)}, nil
}

func (e fakeEncoder) Created(a weather.Assessment, rev int) (ports.OutboxMessage, error) {
	return e.msg("created", a.ID, rev, a.Level.String())
}

func (e fakeEncoder) Updated(a weather.Assessment, rev int, prev hazard.Level) (ports.OutboxMessage, error) {
	return e.msg("updated", a.ID, rev, prev.String()+"→"+a.Level.String())
}

func (e fakeEncoder) Expired(id hazard.EventID, _ time.Time, reason ports.ExpiryReason, into hazard.EventID, rev int) (ports.OutboxMessage, error) {
	return e.msg("expired", id, rev, fmt.Sprintf("%d:%s", reason, into))
}
