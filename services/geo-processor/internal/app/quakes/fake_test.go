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

// memStore meniru semantik adapter PostgreSQL di memori: transaksi dengan
// salinan keadaan (rollback = buang salinan), status kejadian, dan outbox.
type memStore struct {
	state   memState
	regions []quake.RegionDistance
	failOn  string // nama operasi yang dibuat gagal, untuk test galat
}

type memEvent struct {
	rec        ports.EventRecord
	status     ports.EventStatus
	mergedInto quake.EventID
	endedAt    time.Time
}

type memState struct {
	reports map[quake.ReportKey]quake.Stored
	member  map[quake.IdentityKey]quake.EventID
	events  map[quake.EventID]*memEvent
	outbox  []ports.OutboxMessage
}

func newMemStore() *memStore {
	return &memStore{state: memState{
		reports: map[quake.ReportKey]quake.Stored{},
		member:  map[quake.IdentityKey]quake.EventID{},
		events:  map[quake.EventID]*memEvent{},
	}}
}

func (s memState) clone() memState {
	c := memState{
		reports: map[quake.ReportKey]quake.Stored{},
		member:  map[quake.IdentityKey]quake.EventID{},
		events:  map[quake.EventID]*memEvent{},
		outbox:  slices.Clone(s.outbox),
	}
	for k, v := range s.reports {
		c.reports[k] = v
	}
	for k, v := range s.member {
		c.member[k] = v
	}
	for k, v := range s.events {
		cp := *v
		c.events[k] = &cp
	}
	return c
}

var errInjected = errors.New("galat buatan")

func (m *memStore) InTx(_ context.Context, fn func(ports.QuakeTx) error) error {
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

func (t *memTx) LockQuakes(context.Context) error { return t.fail("lock") }

func (t *memTx) Report(_ context.Context, key quake.ReportKey) (*quake.Stored, error) {
	if err := t.fail("report"); err != nil {
		return nil, err
	}
	st, ok := t.st.reports[key]
	if !ok {
		return nil, nil
	}
	return &st, nil
}

func (t *memTx) SaveReport(_ context.Context, r quake.Stored) error {
	if err := t.fail("save-report"); err != nil {
		return err
	}
	t.st.reports[r.Key()] = r
	if r.Deleted() {
		delete(t.st.member, r.Identity()) // sama dengan adapter: laporan ditarik langsung lepas
	}
	return nil
}

func (t *memTx) IdentityReports(_ context.Context, key quake.IdentityKey) ([]quake.Stored, error) {
	var out []quake.Stored
	for k, r := range t.st.reports {
		if k.IdentityKey == key {
			out = append(out, r)
		}
	}
	return out, nil
}

func (t *memTx) EventOf(_ context.Context, key quake.IdentityKey) (quake.EventID, error) {
	return t.st.member[key], nil
}

func (t *memTx) Clusters(_ context.Context, from, to time.Time, include []quake.EventID) (map[quake.EventID][]quake.Stored, error) {
	if err := t.fail("clusters"); err != nil {
		return nil, err
	}
	want := map[quake.EventID]bool{}
	for _, id := range include {
		want[id] = true
	}
	for k, r := range t.st.reports {
		id, ok := t.st.member[k.IdentityKey]
		if !ok || r.OccurredAt.Before(from) || r.OccurredAt.After(to) {
			continue
		}
		if ev := t.st.events[id]; ev != nil && (ev.status == ports.StatusActive || ev.status == ports.StatusExpired) {
			want[id] = true
		}
	}
	out := map[quake.EventID][]quake.Stored{}
	for k, r := range t.st.reports {
		if id, ok := t.st.member[k.IdentityKey]; ok && want[id] {
			out[id] = append(out[id], r)
		}
	}
	return out, nil
}

func (t *memTx) EventExists(_ context.Context, id quake.EventID) (bool, error) {
	_, ok := t.st.events[id]
	return ok, t.fail("exists")
}

func (t *memTx) Event(_ context.Context, id quake.EventID) (ports.StoredEvent, bool, error) {
	ev, ok := t.st.events[id]
	if !ok {
		return ports.StoredEvent{}, false, nil
	}
	return ports.StoredEvent{ID: id, Status: ev.status, Level: ev.rec.Level, Revision: ev.rec.Revision, Digest: ev.rec.Digest}, true, nil
}

func (t *memTx) RegionsWithin(_ context.Context, lat, lon, radiusKm float64) ([]quake.RegionDistance, error) {
	if err := t.fail("regions"); err != nil {
		return nil, err
	}
	// Wilayah uji ditempatkan relatif terhadap episenter; jaraknya tetap.
	var out []quake.RegionDistance
	for _, r := range t.store.regions {
		if r.DistanceKm <= radiusKm {
			out = append(out, r)
		}
	}
	_ = lat
	_ = lon
	return out, nil
}

func (t *memTx) SetMembership(_ context.Context, id quake.EventID, members []quake.IdentityKey) error {
	if _, ok := t.st.events[id]; !ok {
		return fmt.Errorf("FK: kejadian %s belum ada", id)
	}
	for _, k := range members {
		t.st.member[k] = id
	}
	return nil
}

func (t *memTx) Detach(_ context.Context, key quake.IdentityKey) error {
	delete(t.st.member, key)
	return nil
}

func (t *memTx) SaveEvent(_ context.Context, rec ports.EventRecord) error {
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

func (t *memTx) EndEvent(_ context.Context, id quake.EventID, status ports.EventStatus, mergedInto quake.EventID, at time.Time, revision int) error {
	ev, ok := t.st.events[id]
	if !ok {
		return fmt.Errorf("kejadian %s tidak ada", id)
	}
	if !mergedInto.IsZero() {
		if _, ok := t.st.events[mergedInto]; !ok {
			return fmt.Errorf("FK: tujuan gabung %s belum ada", mergedInto)
		}
	}
	ev.status, ev.mergedInto, ev.endedAt, ev.rec.Revision = status, mergedInto, at, revision
	return nil
}

func (t *memTx) DueForExpiry(_ context.Context, now time.Time, limit int) ([]ports.StoredEvent, error) {
	if err := t.fail("due"); err != nil {
		return nil, err
	}
	var out []ports.StoredEvent
	for id, ev := range t.st.events {
		if ev.status == ports.StatusActive && !ev.rec.ExpiresAt.After(now) {
			out = append(out, ports.StoredEvent{ID: id, Status: ev.status, Level: ev.rec.Level, Revision: ev.rec.Revision})
		}
	}
	slices.SortFunc(out, func(a, b ports.StoredEvent) int { return slices.Compare(a.ID[:], b.ID[:]) })
	return out[:min(limit, len(out))], nil
}

func (t *memTx) Enqueue(_ context.Context, msg ports.OutboxMessage) error {
	if err := t.fail("enqueue"); err != nil {
		return err
	}
	for _, m := range t.st.outbox {
		if m.MsgID == msg.MsgID {
			return nil
		}
	}
	t.st.outbox = append(t.st.outbox, msg)
	return nil
}

// fakeEncoder mencatat transisi sebagai subjek dan ID pesan yang mudah dibaca.
type fakeEncoder struct{ failOn string }

func (e fakeEncoder) msg(kind string, id quake.EventID, rev int, extra string) (ports.OutboxMessage, error) {
	if e.failOn == kind {
		return ports.OutboxMessage{}, errInjected
	}
	return ports.OutboxMessage{Subject: "hazard.quake." + kind, MsgID: fmt.Sprintf("%s:r%d", id, rev), Payload: []byte(extra)}, nil
}

func (e fakeEncoder) Created(a quake.Assessment, rev int) (ports.OutboxMessage, error) {
	return e.msg("created", a.ID, rev, a.Level.String())
}

func (e fakeEncoder) Updated(a quake.Assessment, rev int, prev quake.Level) (ports.OutboxMessage, error) {
	return e.msg("updated", a.ID, rev, prev.String()+"→"+a.Level.String())
}

func (e fakeEncoder) Expired(id quake.EventID, _ time.Time, reason ports.ExpiryReason, merged quake.EventID, rev int) (ports.OutboxMessage, error) {
	extra := fmt.Sprintf("reason=%d", reason)
	if !merged.IsZero() {
		extra += " into=" + merged.String()
	}
	return e.msg("expired", id, rev, extra)
}
