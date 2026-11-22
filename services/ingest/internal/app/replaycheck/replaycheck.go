// Package replaycheck membandingkan pesan hasil replay dengan pesan yang
// diterbitkan polling langsung dan masih tersimpan di stream RAW. ID pesan
// adalah hash (konektor, kunci record, isi tanpa FetchMeta), jadi ID yang
// sama berarti replay menghasilkan isi yang persis sama dengan polling.
// Ini uji replay T3 terhadap data asli tanpa menerbitkan apa pun (ADR 0027).
package replaycheck

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Recorder adalah ports.Publisher yang hanya mencatat identitas pesan.
type Recorder struct {
	mu   sync.Mutex
	msgs []ports.StreamMessage
}

// Publish mencatat msg; tidak pernah dianggap duplikat.
func (r *Recorder) Publish(_ context.Context, m ports.Message) (ports.PublishResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, ports.StreamMessage{Subject: m.Subject, ID: m.ID, FetchedAt: m.FetchedAt})
	return ports.PublishResult{}, nil
}

// Messages mengembalikan salinan pesan yang tercatat.
func (r *Recorder) Messages() []ports.StreamMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.msgs)
}

// MaxSamples membatasi contoh pesan yang hanya ada di satu sisi per subjek.
const MaxSamples = 10

// Subject adalah hasil perbandingan satu subjek. Hitungan adalah ID unik:
// pesan ganda ditolak JetStream, jadi polling yang menerbitkan isi sama dua
// kali hanya tersimpan sekali.
type Subject struct {
	Subject    string                `json:"subject"`
	Replayed   int                   `json:"replayed"`
	Live       int                   `json:"live"`
	Matched    int                   `json:"matched"`
	ReplayOnly []ports.StreamMessage `json:"replay_only,omitempty"`
	LiveOnly   []ports.StreamMessage `json:"live_only,omitempty"`
}

// Result adalah hasil perbandingan semua subjek.
type Result struct {
	From     time.Time `json:"from,omitzero"`
	To       time.Time `json:"to,omitzero"`
	Subjects []Subject `json:"subjects"`
}

// Equal benar bila setiap pesan ada di kedua sisi.
func (r Result) Equal() bool {
	for _, s := range r.Subjects {
		if s.Matched != s.Replayed || s.Matched != s.Live {
			return false
		}
	}
	return true
}

// Run membaca pesan stream bersubjek subjects lalu membandingkannya dengan
// pesan replay bersubjek sama dalam rentang waktu ambil [from, to) (nol =
// tanpa batas). Stream dibaca sejak from: pesan disimpan setelah payload-nya
// diambil.
func Run(ctx context.Context, stream ports.StreamReader, replayed []ports.StreamMessage, subjects []string, from, to time.Time) (Result, error) {
	replayed = slices.DeleteFunc(slices.Clone(replayed), func(m ports.StreamMessage) bool { return !slices.Contains(subjects, m.Subject) })
	var live []ports.StreamMessage
	if len(subjects) > 0 {
		for m, err := range stream.Messages(ctx, subjects, from) {
			if err != nil {
				return Result{}, fmt.Errorf("membaca stream: %w", err)
			}
			live = append(live, m)
		}
	}
	return Compare(replayed, live, subjects, from, to), nil
}

// Compare membandingkan dua himpunan pesan per subjek. Waktu ambil dibulatkan
// ke bawah ke detik (presisi kunci arsip) sebelum dicek terhadap rentang.
// Semua subjects dilaporkan walau kosong.
func Compare(replayed, live []ports.StreamMessage, subjects []string, from, to time.Time) Result {
	in := func(m ports.StreamMessage) bool {
		t := m.FetchedAt.Truncate(time.Second)
		return (from.IsZero() || !t.Before(from)) && (to.IsZero() || t.Before(to))
	}
	type sides struct {
		replay, live map[string]ports.StreamMessage
	}
	bySubject := map[string]*sides{}
	get := func(s string) *sides {
		if bySubject[s] == nil {
			bySubject[s] = &sides{replay: map[string]ports.StreamMessage{}, live: map[string]ports.StreamMessage{}}
		}
		return bySubject[s]
	}
	for _, s := range subjects {
		get(s)
	}
	for _, m := range replayed {
		if in(m) {
			get(m.Subject).replay[m.ID] = m
		}
	}
	for _, m := range live {
		if in(m) {
			get(m.Subject).live[m.ID] = m
		}
	}
	res := Result{From: from, To: to}
	for _, name := range slices.Sorted(maps.Keys(bySubject)) {
		s := bySubject[name]
		out := Subject{Subject: name, Replayed: len(s.replay), Live: len(s.live)}
		for id := range s.replay {
			if _, ok := s.live[id]; ok {
				out.Matched++
			}
		}
		out.ReplayOnly = only(s.replay, s.live)
		out.LiveOnly = only(s.live, s.replay)
		res.Subjects = append(res.Subjects, out)
	}
	return res
}

// only mengembalikan contoh pesan a yang tidak ada di b, urut waktu ambil.
func only(a, b map[string]ports.StreamMessage) []ports.StreamMessage {
	var out []ports.StreamMessage
	for id, m := range a {
		if _, ok := b[id]; !ok {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(x, y ports.StreamMessage) int {
		return cmp.Or(x.FetchedAt.Compare(y.FetchedAt), cmp.Compare(x.ID, y.ID))
	})
	return out[:min(len(out), MaxSamples)]
}
