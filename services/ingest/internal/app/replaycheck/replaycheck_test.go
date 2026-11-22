package replaycheck

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

var t0 = time.Date(2026, 11, 20, 3, 0, 0, 0, time.UTC)

func msg(subject, id string, at time.Duration) ports.StreamMessage {
	return ports.StreamMessage{Subject: subject, ID: id, FetchedAt: t0.Add(at)}
}

type stream struct {
	msgs []ports.StreamMessage
	err  error
	got  struct {
		subjects []string
		since    time.Time
	}
}

func (s *stream) Messages(_ context.Context, subjects []string, since time.Time) iter.Seq2[ports.StreamMessage, error] {
	s.got.subjects, s.got.since = subjects, since
	return func(yield func(ports.StreamMessage, error) bool) {
		for _, m := range s.msgs {
			if !yield(m, nil) {
				return
			}
		}
		if s.err != nil {
			yield(ports.StreamMessage{}, s.err)
		}
	}
}

func TestRecorder(t *testing.T) {
	var r Recorder
	res, err := r.Publish(t.Context(), ports.Message{Subject: "raw.a", ID: "1", FetchedAt: t0, Data: []byte("x")})
	if err != nil || res.Duplicate {
		t.Fatal(res, err)
	}
	_, _ = r.Publish(t.Context(), ports.Message{Subject: "raw.a", ID: "1", FetchedAt: t0})
	if got := r.Messages(); len(got) != 2 || got[0] != msg("raw.a", "1", 0) {
		t.Fatalf("%+v", got)
	}
}

func TestCompare(t *testing.T) {
	replayed := []ports.StreamMessage{
		msg("raw.w", "a", 0),
		msg("raw.w", "a", 0), // isi sama terbit dua kali (jalan ulang ingest)
		msg("raw.w", "b", time.Minute),
		msg("raw.w", "r", 2*time.Minute), // hanya replay
		msg("raw.w", "awal", -time.Second),
		msg("raw.w", "akhir", time.Hour),
	}
	live := []ports.StreamMessage{
		msg("raw.w", "a", 0),
		msg("raw.w", "b", time.Minute+900*time.Millisecond), // presisi lebih halus dari kunci arsip
		msg("raw.w", "l", 3*time.Minute),                    // hanya polling (arsip gagal)
		msg("raw.q", "q", 0),
		msg("raw.w", "akhir", time.Hour-time.Millisecond), // detiknya masih di dalam rentang
	}
	res := Compare(replayed, live, []string{"raw.w", "raw.kosong"}, t0, t0.Add(time.Hour))
	if res.Equal() {
		t.Fatal("seharusnya berbeda")
	}
	want := []Subject{
		{Subject: "raw.kosong"},
		{Subject: "raw.q", Live: 1, LiveOnly: []ports.StreamMessage{msg("raw.q", "q", 0)}},
		{
			Subject: "raw.w", Replayed: 3, Live: 4, Matched: 2,
			ReplayOnly: []ports.StreamMessage{msg("raw.w", "r", 2*time.Minute)},
			LiveOnly:   []ports.StreamMessage{msg("raw.w", "l", 3*time.Minute), msg("raw.w", "akhir", time.Hour-time.Millisecond)},
		},
	}
	if fmt.Sprint(res.Subjects) != fmt.Sprint(want) {
		t.Fatalf("\n%+v\ningin\n%+v", res.Subjects, want)
	}
	if !Compare(replayed[:2], live[:1], nil, time.Time{}, time.Time{}).Equal() {
		t.Fatal("seharusnya sama")
	}
	var many []ports.StreamMessage
	for i := range MaxSamples + 5 {
		many = append(many, msg("raw.w", fmt.Sprint(i), time.Duration(i)*time.Second))
	}
	if got := Compare(many, nil, nil, time.Time{}, time.Time{}).Subjects[0]; got.Replayed != MaxSamples+5 || len(got.ReplayOnly) != MaxSamples {
		t.Fatalf("%+v", got)
	}
}

func TestRun(t *testing.T) {
	s := &stream{msgs: []ports.StreamMessage{msg("raw.w", "a", 0)}}
	// Pesan replay bersubjek lain tidak dibandingkan.
	res, err := Run(t.Context(), s, []ports.StreamMessage{msg("raw.w", "a", 0), msg("raw.lain", "x", 0)}, []string{"raw.w"}, t0, time.Time{})
	if err != nil || !res.Equal() || !slices.Equal(s.got.subjects, []string{"raw.w"}) || !s.got.since.Equal(t0) {
		t.Fatalf("%+v %v %+v", res, err, s.got)
	}
	s.err = errors.New("NATS mati")
	if _, err := Run(t.Context(), s, nil, []string{"raw.w"}, t0, time.Time{}); !errors.Is(err, s.err) {
		t.Fatalf("galat %v", err)
	}
	// Tanpa subjek stream tidak dibaca.
	s = &stream{err: errors.New("tidak boleh dibaca")}
	if res, err := Run(t.Context(), s, []ports.StreamMessage{msg("raw.w", "a", 0)}, nil, t0, time.Time{}); err != nil || !res.Equal() || len(res.Subjects) != 0 {
		t.Fatal(res, err)
	}
}
