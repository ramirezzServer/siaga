package fires

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// memStore meniru semantik adapter PostgreSQL di memori: transaksi dengan
// salinan keadaan, status kejadian, anggota, dan outbox.
type memStore struct {
	state  memState
	failOn string
	synced int
}

type memEvent struct {
	rec        ports.FireEventRecord
	status     ports.EventStatus
	mergedInto hazard.EventID
	endedAt    time.Time
}

type memState struct {
	events  map[hazard.EventID]*memEvent
	members map[string]ports.FireMember
	outbox  []ports.OutboxMessage
}

func newMemStore() *memStore {
	return &memStore{state: memState{events: map[hazard.EventID]*memEvent{}, members: map[string]ports.FireMember{}}}
}

func (s memState) clone() memState {
	c := memState{events: map[hazard.EventID]*memEvent{}, members: maps.Clone(s.members), outbox: slices.Clone(s.outbox)}
	for k, v := range s.events {
		cp := *v
		c.events[k] = &cp
	}
	return c
}

var errInjected = errors.New("galat buatan")

func (s *memStore) InTx(_ context.Context, fn func(ports.FireTx) error) error {
	tx := &memTx{st: s.state.clone(), failOn: s.failOn}
	if err := fn(tx); err != nil {
		return err
	}
	s.state = tx.st
	return nil
}

func (s *memStore) SyncStatic(_ context.Context, cal fire.Calibration) (ports.ThresholdSync, error) {
	if s.failOn == "sync" {
		return ports.ThresholdSync{}, errInjected
	}
	s.synced++
	return ports.ThresholdSync{Inserted: len(cal.Static)}, nil
}

// active mengembalikan kejadian aktif.
func (s *memStore) active() []*memEvent {
	var out []*memEvent
	for _, e := range s.state.events {
		if e.status == ports.StatusActive {
			out = append(out, e)
		}
	}
	return out
}

type memTx struct {
	st     memState
	failOn string
}

func (t *memTx) fail(op string) error {
	if t.failOn == op {
		return fmt.Errorf("%s: %w", op, errInjected)
	}
	return nil
}

func (t *memTx) LockFire(context.Context) error { return t.fail("lock") }

func (t *memTx) HasDetection(_ context.Context, id string) (bool, error) {
	_, ok := t.st.members[id]
	return ok, t.fail("has")
}

func (t *memTx) Nearby(_ context.Context, d fire.Detection, gap time.Duration, km float64) ([]ports.FireMember, error) {
	var out []ports.FireMember
	for _, m := range t.st.members {
		e := t.st.events[m.EventID]
		dt := m.DetectedAt.Sub(d.DetectedAt)
		if e.status == ports.StatusActive && dt <= gap && dt >= -gap && fire.DistanceKm(d.Lat, d.Lon, m.Lat, m.Lon) <= km {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b ports.FireMember) int { return a.DetectedAt.Compare(b.DetectedAt) })
	return out, t.fail("nearby")
}

func (t *memTx) Episode(_ context.Context, id hazard.EventID) (ports.FireEpisode, bool, error) {
	e, ok := t.st.events[id]
	if !ok {
		return ports.FireEpisode{}, false, t.fail("episode")
	}
	a := e.rec.Assessment
	return ports.FireEpisode{
		StoredEvent:    ports.StoredEvent{ID: id, Status: e.status, Level: a.Level, Revision: e.rec.Revision, Digest: e.rec.Digest},
		OpenedBy:       a.OpenedBy,
		FirstDetected:  a.First,
		ExpiresAt:      a.ExpiresAt,
		DetectionCount: a.Detections,
	}, true, t.fail("episode")
}

func (t *memTx) Members(_ context.Context, id hazard.EventID) ([]fire.Detection, error) {
	var out []fire.Detection
	for _, m := range t.st.members {
		if m.EventID == id {
			out = append(out, m.Detection)
		}
	}
	fire.Sort(out)
	return out, t.fail("members")
}

func (t *memTx) SaveEvent(_ context.Context, rec ports.FireEventRecord) error {
	if e, ok := t.st.events[rec.ID]; ok {
		e.rec = rec
	} else {
		t.st.events[rec.ID] = &memEvent{rec: rec, status: ports.StatusActive}
	}
	return t.fail("save")
}

func (t *memTx) AddDetection(_ context.Context, ev hazard.EventID, d fire.Detection) error {
	if _, ok := t.st.events[ev]; !ok {
		return fmt.Errorf("kejadian %s tidak ada", ev)
	}
	t.st.members[d.ID] = ports.FireMember{EventID: ev, Detection: d}
	return t.fail("add")
}

func (t *memTx) MoveDetections(_ context.Context, from, to hazard.EventID) error {
	for id, m := range t.st.members {
		if m.EventID == from {
			m.EventID = to
			t.st.members[id] = m
		}
	}
	return t.fail("move")
}

func (t *memTx) EndEvent(_ context.Context, id hazard.EventID, status ports.EventStatus, into hazard.EventID, at time.Time, rev int) error {
	e, ok := t.st.events[id]
	if !ok {
		return fmt.Errorf("kejadian %s tidak ada", id)
	}
	e.status, e.mergedInto, e.endedAt, e.rec.Revision = status, into, at, rev
	return t.fail("end")
}

func (t *memTx) DueForExpiry(_ context.Context, now time.Time, limit int) ([]ports.StoredEvent, error) {
	var out []ports.StoredEvent
	for id, e := range t.st.events {
		if e.status == ports.StatusActive && !e.rec.ExpiresAt.After(now) {
			out = append(out, ports.StoredEvent{ID: id, Status: e.status, Level: e.rec.Level, Revision: e.rec.Revision})
		}
	}
	slices.SortFunc(out, func(a, b ports.StoredEvent) int { return a.ID.Compare(b.ID) })
	return out[:min(limit, len(out))], t.fail("due")
}

func (t *memTx) Enqueue(_ context.Context, msg ports.OutboxMessage) error {
	if slices.ContainsFunc(t.st.outbox, func(o ports.OutboxMessage) bool { return o.MsgID == msg.MsgID }) {
		return nil
	}
	t.st.outbox = append(t.st.outbox, msg)
	return t.fail("enqueue")
}

// fakeEncoder menulis subjek dan ID pesan yang bisa diperiksa test.
type fakeEncoder struct{ fail bool }

func (e fakeEncoder) msg(kind string, id hazard.EventID, rev int, extra string) (ports.OutboxMessage, error) {
	if e.fail {
		return ports.OutboxMessage{}, errInjected
	}
	return ports.OutboxMessage{Subject: "hazard.fire." + kind, MsgID: fmt.Sprintf("%s:r%d", id, rev), Payload: []byte(extra)}, nil
}

func (e fakeEncoder) Created(a fire.Assessment, rev int) (ports.OutboxMessage, error) {
	return e.msg("created", a.ID, rev, a.Level.String())
}

func (e fakeEncoder) Updated(a fire.Assessment, rev int, prev hazard.Level) (ports.OutboxMessage, error) {
	return e.msg("updated", a.ID, rev, prev.String()+"→"+a.Level.String())
}

func (e fakeEncoder) Expired(id hazard.EventID, _ time.Time, reason ports.ExpiryReason, into hazard.EventID, rev int) (ports.OutboxMessage, error) {
	return e.msg("expired", id, rev, fmt.Sprint(reason, " ", into))
}
