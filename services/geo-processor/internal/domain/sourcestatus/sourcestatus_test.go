package sourcestatus

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
)

var t0 = time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)

func poll(at time.Duration, ok, changed bool) Poll {
	p := Poll{
		Connector: "bmkg-autogempa", Subject: "raw.quake.bmkg", Interval: 30 * time.Second,
		StartedAt: t0.Add(at), FinishedAt: t0.Add(at + 500*time.Millisecond), OK: ok,
	}
	if changed {
		p.Published = 1
	}
	return p
}

func TestValidate(t *testing.T) {
	now := t0.Add(time.Hour)
	if err := poll(0, true, true).Validate(now); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Poll){
		"konektor kosong":    func(p *Poll) { p.Connector = "" },
		"konektor besar":     func(p *Poll) { p.Connector = "BMKG" },
		"konektor dua titik": func(p *Poll) { p.Connector = "usgs-2..5" },
		"konektor panjang":   func(p *Poll) { p.Connector = strings.Repeat("a", MaxConnectorLen+1) },
		"subjek bukan raw":   func(p *Poll) { p.Subject = "hazard.quake.created" },
		"sumber tak dikenal": func(p *Poll) { p.Subject = "raw.quake.emsc" },
		"interval nol":       func(p *Poll) { p.Interval = 0 },
		"interval terlalu":   func(p *Poll) { p.Interval = MaxInterval + time.Second },
		"tanpa waktu mulai":  func(p *Poll) { p.StartedAt = time.Time{} },
		"selesai sebelum":    func(p *Poll) { p.FinishedAt = p.StartedAt.Add(-time.Second) },
		"terlalu lama":       func(p *Poll) { p.FinishedAt = p.StartedAt.Add(MaxDuration + time.Second) },
		"masa depan":         func(p *Poll) { p.StartedAt, p.FinishedAt = now.Add(time.Hour), now.Add(time.Hour) },
		"gagal tapi berubah": func(p *Poll) { p.OK = false },
		"gagal tapi terbit":  func(p *Poll) { p.OK = false },
		"terbit negatif":     func(p *Poll) { p.Published = -1 },
	} {
		p := poll(0, true, true)
		mutate(&p)
		if err := p.Validate(now); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, ingin ErrInvalid", name, err)
		}
	}
	// Nama konektor boleh bertitik (feed USGS).
	usgs := poll(0, true, true)
	usgs.Connector, usgs.Subject = "usgs-2.5-day", "raw.quake.usgs"
	if err := usgs.Validate(now); err != nil {
		t.Fatalf("usgs-2.5-day: %v", err)
	}
	// Selisih jam kecil ditoleransi.
	if err := poll(time.Hour+5*time.Minute, true, false).Validate(now); err != nil {
		t.Fatalf("selisih jam 5 menit harus diterima: %v", err)
	}
}

// Subjek yang diterima sama dengan jenis dan sumber di kontrak streams.
func TestSubjectsMatchStreams(t *testing.T) {
	kinds := []streams.Kind{streams.KindQuake, streams.KindWeather, streams.KindForecast, streams.KindAQ, streams.KindFlood, streams.KindRain, streams.KindFire}
	sources := []streams.Source{streams.SourceBMKG, streams.SourceUSGS, streams.SourceOpenMeteo, streams.SourceOpenAQ, streams.SourceFIRMS}
	for _, k := range kinds {
		for _, s := range sources {
			sub, err := streams.RawSubject(k, s)
			if err != nil {
				t.Fatal(err)
			}
			if !subjectFormat.MatchString(sub) {
				t.Errorf("subjek %s ditolak", sub)
			}
		}
	}
}

func TestApplySequence(t *testing.T) {
	s, ok := Apply(nil, poll(0, true, true))
	if !ok || !s.LastAttemptAt.Equal(t0.Add(500*time.Millisecond)) || !s.LastSuccessAt.Equal(s.LastAttemptAt) ||
		!s.LastChangeAt.Equal(s.LastAttemptAt) || s.ConsecutiveFailures != 0 || !s.FailingSince.IsZero() {
		t.Fatalf("pertama %+v", s)
	}
	changedAt := s.LastChangeAt
	// Berhasil tanpa isi baru: waktu berhasil maju, waktu berubah tetap.
	s, _ = Apply(&s, poll(30*time.Second, true, false))
	if !s.LastChangeAt.Equal(changedAt) || !s.LastSuccessAt.Equal(t0.Add(30500*time.Millisecond)) {
		t.Fatalf("tanpa isi baru %+v", s)
	}
	lastOK := s.LastSuccessAt
	// Tiga kali gagal: gagal sejak = akhir kegagalan pertama.
	for i := range 3 {
		s, _ = Apply(&s, poll(time.Duration(i+2)*time.Minute, false, false))
		mustValid(t, s)
	}
	if s.ConsecutiveFailures != 3 || !s.FailingSince.Equal(t0.Add(2*time.Minute+500*time.Millisecond)) ||
		!s.LastSuccessAt.Equal(lastOK) || !s.LastAttemptAt.Equal(t0.Add(4*time.Minute+500*time.Millisecond)) {
		t.Fatalf("setelah gagal %+v", s)
	}
	// Pesan ulangan dan pesan terlambat diabaikan.
	for _, p := range []Poll{poll(4*time.Minute, false, false), poll(time.Minute, true, true)} {
		if got, ok := Apply(&s, p); ok || got != s {
			t.Fatalf("pesan lama mengubah keadaan: %+v", got)
		}
	}
	// Pulih; konfigurasi baru (interval, subjek) mengikuti pesan terbaru.
	p := poll(10*time.Minute, true, true)
	p.Interval, p.Subject = time.Minute, "raw.quake.usgs"
	s, _ = Apply(&s, p)
	mustValid(t, s)
	if s.ConsecutiveFailures != 0 || !s.FailingSince.IsZero() || s.Interval != time.Minute || s.Subject != "raw.quake.usgs" ||
		!s.LastChangeAt.Equal(s.LastAttemptAt) {
		t.Fatalf("pulih %+v", s)
	}
	// Konektor yang gagal sejak percobaan pertama: belum pernah berhasil.
	s, _ = Apply(nil, poll(0, false, false))
	mustValid(t, s)
	if !s.LastSuccessAt.IsZero() || s.ConsecutiveFailures != 1 || !s.FailingSince.Equal(s.LastAttemptAt) {
		t.Fatalf("gagal pertama %+v", s)
	}
}

func TestStateValidateRejects(t *testing.T) {
	good, _ := Apply(nil, poll(0, true, true))
	mustValid(t, good)
	for name, mutate := range map[string]func(*State){
		"konektor": func(s *State) { s.Connector = "x y" },
		"subjek":   func(s *State) { s.Subject = "raw.x.bmkg" },
		"interval": func(s *State) { s.Interval = 0 },
		"belum dicoba": func(s *State) {
			s.LastAttemptAt, s.LastSuccessAt, s.LastChangeAt = time.Time{}, time.Time{}, time.Time{}
		},
		"berhasil setelah":  func(s *State) { s.LastSuccessAt = s.LastAttemptAt.Add(time.Second) },
		"berubah setelah":   func(s *State) { s.LastChangeAt = s.LastSuccessAt.Add(time.Second) },
		"berubah tanpa ok":  func(s *State) { s.LastSuccessAt = time.Time{} },
		"gagal tanpa sejak": func(s *State) { s.ConsecutiveFailures = 1 },
		"sejak tanpa gagal": func(s *State) { s.FailingSince = s.LastAttemptAt },
		"ok tapi lama":      func(s *State) { s.LastAttemptAt = s.LastAttemptAt.Add(time.Minute) },
		"sejak sebelum ok":  func(s *State) { s.ConsecutiveFailures, s.FailingSince = 1, s.LastSuccessAt },
		"gagal negatif":     func(s *State) { s.ConsecutiveFailures = -1 },
	} {
		s := good
		mutate(&s)
		if err := s.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func mustValid(t *testing.T, s State) {
	t.Helper()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

// FuzzApplyInvariants: urutan hasil polling apa pun (termasuk ulangan dan
// pesan terlambat) selalu menghasilkan keadaan yang valid dengan waktu coba
// yang tidak pernah mundur. Bila urutannya kronologis, keadaan akhir sama
// dengan hitungan langsung: waktu berhasil terakhir dan kegagalan di ekor.
func FuzzApplyInvariants(f *testing.F) {
	f.Add([]byte{0x11, 0x20, 0x02, 0x33, 0x04})
	f.Add([]byte{0x00, 0x00, 0x01, 0xff, 0x80})
	f.Fuzz(func(t *testing.T, data []byte) {
		polls := make([]Poll, 0, len(data))
		for _, b := range data {
			// Bit 0: berhasil; bit 1: isi baru; sisanya: menit ke berapa.
			ok := b&1 == 1
			polls = append(polls, poll(time.Duration(b>>2)*time.Minute, ok, ok && b&2 == 2))
		}
		var s *State
		for _, p := range polls {
			next, _ := Apply(s, p)
			if err := next.Validate(); err != nil {
				t.Fatalf("keadaan tidak valid setelah %+v: %v", p, err)
			}
			if s != nil && next.LastAttemptAt.Before(s.LastAttemptAt) {
				t.Fatal("waktu coba mundur")
			}
			s = &next
		}

		sorted := slices.Clone(polls)
		slices.SortStableFunc(sorted, func(a, b Poll) int { return a.FinishedAt.Compare(b.FinishedAt) })
		var st *State
		for _, p := range sorted {
			next, _ := Apply(st, p)
			st = &next
		}
		if st == nil {
			return
		}
		// Pesan dengan waktu sama: yang pertama tiba yang dipakai.
		var lastOK time.Time
		var tail int
		var prev time.Time
		for i, p := range sorted {
			if i > 0 && p.FinishedAt.Equal(prev) {
				continue
			}
			prev = p.FinishedAt
			if p.OK {
				lastOK, tail = p.FinishedAt, 0
			} else {
				tail++
			}
		}
		if !st.LastSuccessAt.Equal(lastOK) || st.ConsecutiveFailures != tail {
			t.Fatalf("keadaan %+v, ingin berhasil %v dan %d gagal di ekor", st, lastOK, tail)
		}
	})
}
