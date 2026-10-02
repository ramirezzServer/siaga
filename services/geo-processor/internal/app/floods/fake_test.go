package floods

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/flood"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// memStore meniru semantik adapter PostgreSQL di memori: transaksi dengan
// salinan keadaan, status kejadian, keadaan titik, dan outbox.
type memStore struct {
	state  memState
	failOn string
	synced int
}

type memEvent struct {
	rec     ports.FloodEventRecord
	status  ports.EventStatus
	endedAt time.Time
}

type memState struct {
	sites  map[string]ports.FloodSite
	events map[hazard.EventID]*memEvent
	outbox []ports.OutboxMessage
}

func newMemStore() *memStore {
	return &memStore{state: memState{sites: map[string]ports.FloodSite{}, events: map[hazard.EventID]*memEvent{}}}
}

func (s memState) clone() memState {
	c := memState{sites: map[string]ports.FloodSite{}, events: map[hazard.EventID]*memEvent{}, outbox: slices.Clone(s.outbox)}
	for k, v := range s.sites {
		c.sites[k] = v
	}
	for k, v := range s.events {
		cp := *v
		c.events[k] = &cp
	}
	return c
}

var errInjected = errors.New("galat buatan")

func (s *memStore) InTx(_ context.Context, fn func(ports.FloodTx) error) error {
	tx := &memTx{st: s.state.clone(), failOn: s.failOn}
	if err := fn(tx); err != nil {
		return err
	}
	s.state = tx.st
	return nil
}

func (s *memStore) SyncThresholds(_ context.Context, cal flood.Calibration) (ports.ThresholdSync, error) {
	if s.failOn == "sync" {
		return ports.ThresholdSync{}, errInjected
	}
	s.synced++
	return ports.ThresholdSync{Inserted: len(cal.Discharge) + 3*len(cal.Rainfall)}, nil
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

func (t *memTx) LockFlood(context.Context) error { return t.fail("lock") }

func (t *memTx) Site(_ context.Context, id string) (ports.FloodSite, bool, error) {
	st, ok := t.st.sites[id]
	return st, ok, t.fail("site")
}

func (t *memTx) SaveSite(_ context.Context, id string, at time.Time, ev hazard.EventID) error {
	t.st.sites[id] = ports.FloodSite{LastFetchedAt: at, EventID: ev}
	return t.fail("savesite")
}

func (t *memTx) Episode(_ context.Context, id hazard.EventID) (ports.FloodEpisode, bool, error) {
	e, ok := t.st.events[id]
	if !ok {
		return ports.FloodEpisode{}, false, t.fail("episode")
	}
	a := e.rec.Assessment
	return ports.FloodEpisode{
		StoredEvent:    ports.StoredEvent{ID: id, Status: e.status, Level: a.Level, Revision: e.rec.Revision, Digest: e.rec.Digest},
		OpenedAt:       a.OpenedAt,
		LastExceededAt: a.LastExceededAt,
		ExpiresAt:      a.ExpiresAt,
	}, true, t.fail("episode")
}

func (t *memTx) SaveEvent(_ context.Context, rec ports.FloodEventRecord) error {
	if e, ok := t.st.events[rec.ID]; ok {
		e.rec = rec
	} else {
		t.st.events[rec.ID] = &memEvent{rec: rec, status: ports.StatusActive}
	}
	return t.fail("save")
}

func (t *memTx) EndEvent(_ context.Context, id hazard.EventID, status ports.EventStatus, _ hazard.EventID, at time.Time, rev int) error {
	e, ok := t.st.events[id]
	if !ok {
		return fmt.Errorf("kejadian %s tidak ada", id)
	}
	e.status, e.endedAt, e.rec.Revision = status, at, rev
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
	return ports.OutboxMessage{Subject: "hazard.flood." + kind, MsgID: fmt.Sprintf("%s:r%d", id, rev), Payload: []byte(extra)}, nil
}

func (e fakeEncoder) Created(a flood.Assessment, rev int) (ports.OutboxMessage, error) {
	return e.msg("created", a.ID, rev, a.Level.String())
}

func (e fakeEncoder) Updated(a flood.Assessment, rev int, prev hazard.Level) (ports.OutboxMessage, error) {
	return e.msg("updated", a.ID, rev, prev.String()+"→"+a.Level.String())
}

func (e fakeEncoder) Expired(id hazard.EventID, _ time.Time, reason ports.ExpiryReason, _ hazard.EventID, rev int) (ports.OutboxMessage, error) {
	return e.msg("expired", id, rev, fmt.Sprint(reason))
}
