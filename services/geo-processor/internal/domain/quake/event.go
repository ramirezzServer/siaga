package quake

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// View adalah keadaan satu kejadian gempa yang diturunkan dari anggotanya.
// Semua field adalah fungsi murni dari anggota dan Policy, jadi kejadian bisa
// dihitung ulang kapan saja dengan hasil yang sama.
type View struct {
	ID EventID
	// Primary adalah solusi yang berlaku dari sumber berprioritas tertinggi.
	Primary Solution
	// Corroborating adalah solusi yang berlaku dari sumber lain.
	Corroborating []Solution
	DetectedAt    time.Time
	ExpiresAt     time.Time
	// FeltRadiusKm adalah radius dirasakan di permukaan (0 = tidak dirasakan).
	FeltRadiusKm float64
	Title        string
	Summary      string
}

// ErrNoCurrent berarti kejadian tidak punya solusi yang berlaku.
var ErrNoCurrent = errors.New("kejadian tanpa solusi yang berlaku")

// Derive menghitung View dari anggota kejadian.
func (p Policy) Derive(id EventID, members []Solution) (View, error) {
	cur := Current(members)
	if len(cur) == 0 {
		return View{}, fmt.Errorf("%w: %s", ErrNoCurrent, id)
	}
	v := View{ID: id, Primary: cur[0], Corroborating: cur[1:]}
	v.DetectedAt = v.Primary.FirstSeenAt
	for _, m := range members {
		if m.FirstSeenAt.Before(v.DetectedAt) {
			v.DetectedAt = m.FirstSeenAt
		}
	}
	v.ExpiresAt = v.Primary.OccurredAt.Add(p.ActiveFor)
	_, v.FeltRadiusKm = p.FeltRadius(v.Primary.Magnitude, v.Primary.DepthKm)
	v.Title = title(v.Primary)
	v.Summary = summary(v.Primary, v.FeltRadiusKm)
	return v, nil
}

// Assessment adalah View beserta dampaknya, bentuk lengkap yang disimpan
// dan diterbitkan.
type Assessment struct {
	View
	Level   Level
	Impacts []Impact
}

// Digest adalah SHA-256 semua isi yang diterbitkan. Kejadian hanya
// diterbitkan ulang (hazard.quake.updated) bila digest berubah, jadi pesan
// raw yang sama diproses berkali-kali tidak menghasilkan event baru.
func (a Assessment) Digest() [32]byte {
	d := newDigester("siaga/quake-assessment/v1")
	sol := func(s Solution) {
		d.str(string(s.Key.Source))
		d.str(s.Key.EventID)
		d.time(s.OccurredAt)
		d.f64(s.Latitude)
		d.f64(s.Longitude)
		d.f64(s.Magnitude)
		d.str(s.MagnitudeType)
		d.f64(s.DepthKm)
		d.str(s.Place)
		d.str(s.Felt)
		d.int(int64(s.Tsunami))
		d.str(s.ShakemapURL)
		d.str(s.SourceURL)
	}
	d.str(a.ID.String())
	sol(a.Primary)
	d.int(int64(len(a.Corroborating)))
	for _, c := range a.Corroborating {
		sol(c)
	}
	d.time(a.DetectedAt)
	d.time(a.ExpiresAt)
	d.f64(a.FeltRadiusKm)
	d.str(a.Title)
	d.str(a.Summary)
	d.int(int64(a.Level))
	d.int(int64(len(a.Impacts)))
	for _, im := range a.Impacts {
		d.str(im.Code)
		d.str(im.Name)
		d.f64(im.DistanceKm)
		d.int(int64(im.Level))
		if im.WithinFelt {
			d.int(1)
		} else {
			d.int(0)
		}
	}
	return d.sum()
}

// FormatMagnitude menulis magnitudo gaya Indonesia dengan satu desimal: "5,1".
func FormatMagnitude(m float64) string {
	return strings.Replace(strconv.FormatFloat(m, 'f', 1, 64), ".", ",", 1)
}

func formatKm(km float64) string {
	return strconv.FormatFloat(math.Round(km), 'f', 0, 64) + " km"
}

// placeText membuang awalan baku BMKG supaya judul ringkas:
// "Pusat gempa berada di laut 48 km utara Ruteng" → "laut 48 km utara Ruteng".
func placeText(place string) string {
	p := strings.TrimSpace(place)
	for _, prefix := range []string{"Pusat gempa berada di ", "Pusat gempa berada "} {
		if len(p) > len(prefix) && strings.EqualFold(p[:len(prefix)], prefix) {
			return strings.TrimSpace(p[len(prefix):])
		}
	}
	return p
}

func title(s Solution) string {
	t := "Gempa M" + FormatMagnitude(s.Magnitude)
	if place := placeText(s.Place); place != "" {
		t += " — " + place
	}
	return t
}

func summary(s Solution, feltKm float64) string {
	var parts []string
	parts = append(parts, "Kedalaman "+formatKm(math.Max(s.DepthKm, 0))+".")
	if s.Felt != "" {
		parts = append(parts, "Dirasakan (skala MMI): "+strings.TrimSpace(s.Felt)+".")
	}
	if feltKm > 0 {
		parts = append(parts, "Estimasi radius dirasakan "+formatKm(feltKm)+".")
	} else {
		parts = append(parts, "Estimasi: tidak dirasakan di permukaan.")
	}
	switch s.Tsunami {
	case TsunamiPotential:
		parts = append(parts, "BMKG: berpotensi tsunami.")
	case TsunamiNone:
		parts = append(parts, "Tidak berpotensi tsunami.")
	case TsunamiUnknown:
	}
	switch s.Key.Source {
	case SourceBMKG:
		parts = append(parts, "Sumber: BMKG.")
	case SourceUSGS:
		parts = append(parts, "Sumber: USGS.")
	}
	return strings.Join(parts, " ")
}
