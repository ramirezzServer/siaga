package flood

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/weather"
)

// EventIDFor membentuk ID kejadian banjir dari titik dan waktu terima
// keluaran model yang membuka kejadian. Satu titik punya paling banyak satu
// kejadian aktif; kejadian berikutnya dibuka keluaran lain, jadi ID-nya beda.
func EventIDFor(siteID string, opened time.Time) hazard.EventID {
	return hazard.NewEventID("siaga/hazard/flood", 0, siteID, opened.UTC().Format(time.RFC3339Nano))
}

// Assessment adalah kejadian banjir lengkap yang disimpan dan diterbitkan:
// penilaian keluaran terbaru beserta riwayat kejadiannya.
type Assessment struct {
	Outlook
	ID hazard.EventID
	// Level adalah tingkat kejadian: tingkat keluaran terbaru, paling rendah
	// Info selama kejadian aktif (keluaran terbaru bisa sudah di bawah ambang
	// sementara kejadian menunggu 24 jam sebelum berakhir).
	Level hazard.Level
	// OpenedAt adalah waktu terima keluaran yang membuka kejadian; dipakai
	// sebagai waktu kejadian dan waktu deteksi.
	OpenedAt time.Time
	// LastExceededAt adalah waktu terima keluaran terakhir yang mencapai Info.
	LastExceededAt time.Time
	ExpiresAt      time.Time
	Title, Summary string
}

// Assess membentuk kejadian dari keluaran o. lastExceeded adalah waktu
// terima keluaran terakhir yang mencapai Info (o.FetchedAt bila o sendiri
// mencapainya).
func (p Policy) Assess(id hazard.EventID, o Outlook, opened, lastExceeded time.Time) Assessment {
	a := Assessment{
		Outlook: o, ID: id, Level: max(o.Level, hazard.LevelInfo),
		OpenedAt: opened.UTC(), LastExceededAt: lastExceeded.UTC(), ExpiresAt: lastExceeded.UTC().Add(p.Linger),
	}
	a.Title = title(o)
	a.Summary = summary(a)
	return a
}

var levelLabel = [...]string{"di bawah Info", "Info", "Waspada", "Siaga", "Bahaya"}

// Label mengembalikan nama tingkat dalam bahasa Indonesia.
func Label(l hazard.Level) string {
	if l < None || int(l) >= len(levelLabel) {
		return l.String()
	}
	return levelLabel[l]
}

var months = [...]string{"Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"}

// FormatDate menulis tanggal UTC gaya Indonesia, misal "3 Okt 2026".
func FormatDate(t time.Time) string {
	u := t.UTC()
	return fmt.Sprintf("%d %s %d", u.Day(), months[u.Month()-1], u.Year())
}

// num menulis angka satu desimal dengan koma, misal "329,5".
func num(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', 1, 64), ".", ",", 1)
}

func title(o Outlook) string {
	if o.Indicator == Rainfall {
		return "Indikasi potensi banjir sub-DAS " + o.SiteName
	}
	return "Potensi banjir " + o.River + " di " + o.SiteName
}

func summary(a Assessment) string {
	var parts []string
	peak := a.peakDay()
	switch {
	case a.Outlook.Level == None:
		parts = append(parts, fmt.Sprintf("Prakiraan terbaru (%s) di bawah ambang Info; kejadian berakhir %s bila tidak naik lagi.",
			weather.FormatWIB(a.FetchedAt), weather.FormatWIB(a.ExpiresAt)))
	case a.Indicator == Discharge:
		set := a.DischargeThresholds
		base := min(set.Level(*peak.Discharge), a.MaxLevel)
		s := fmt.Sprintf("Debit model di titik pantau %s diprakirakan %s m³/s pada %s", a.SiteName, num(*peak.Discharge), FormatDate(peak.Date))
		switch {
		case peak.Possible && base == None:
			s += fmt.Sprintf(" (di bawah ambang Info %s m³/s); P75 ensemble %s m³/s, jadi kemungkinan %s.",
				threshold(set, hazard.LevelInfo), num(*peak.P75), Label(peak.Level))
		case peak.Possible:
			s += fmt.Sprintf(" (tingkat %s, ambang %s m³/s); P75 ensemble %s m³/s, jadi kemungkinan %s.",
				Label(base), threshold(set, base), num(*peak.P75), Label(peak.Level))
		default:
			s += fmt.Sprintf(", tingkat %s (ambang %s m³/s).", Label(peak.Level), threshold(set, peak.Level))
		}
		parts = append(parts, s)
		if peak.Max != nil {
			parts = append(parts, fmt.Sprintf("Skenario terburuk ensemble %s m³/s.", num(*peak.Max)))
		}
	default:
		w := indexOf(peak.Window)
		parts = append(parts, fmt.Sprintf("Hujan rata-rata wilayah sub-DAS %s diprakirakan %s mm dalam %d jam pada %s, tingkat %s (ambang %s mm).",
			a.SiteName, num(*peak.Rain[w]), peak.Window, FormatDate(peak.Date), Label(peak.Level), threshold(a.RainfallThresholds[w], peak.Level)))
	}
	if a.Indicator == Discharge {
		if a.MaxLevel < hazard.LevelBahaya {
			parts = append(parts, fmt.Sprintf("Debit di titik ini diatur waduk; tingkat dibatasi %s.", Label(a.MaxLevel)))
		}
		parts = append(parts, "Debit model GloFAS, bukan pengukuran lapangan. Sumber: Open-Meteo.")
	} else {
		parts = append(parts, "Indikasi potensi banjir dari hujan model ECMWF IFS, bukan pengukuran debit. Sumber: Open-Meteo.")
	}
	return strings.Join(parts, " ")
}

// peakDay mengembalikan hari Peak dari Days.
func (a Assessment) peakDay() Day {
	for _, d := range a.Days {
		if d.Date.Equal(a.Peak) {
			return d
		}
	}
	return Day{}
}

func threshold(s Set, l hazard.Level) string {
	if l < hazard.LevelInfo || l > hazard.LevelBahaya {
		return "-"
	}
	return num(s[l-1])
}

func indexOf(hours int) int {
	for i, h := range Windows {
		if h == hours {
			return i
		}
	}
	return 0
}

// Digest adalah SHA-256 semua isi yang diterbitkan; kejadian hanya
// diterbitkan ulang bila digest berubah.
func (a Assessment) Digest() [32]byte {
	d := hazard.NewDigester("siaga/flood-assessment/v1")
	d.Str(a.ID.String())
	d.Str(string(a.Indicator))
	d.Str(a.SiteID)
	d.Str(a.SiteName)
	d.Str(a.River)
	d.Str(a.Model)
	d.F64(a.Location.Lat)
	d.F64(a.Location.Lon)
	d.Time(a.FetchedAt)
	d.Int(int64(a.Level))
	d.Int(int64(a.Outlook.Level))
	d.Bool(a.Possible)
	d.Time(a.Peak)
	d.Int(int64(a.MaxLevel))
	d.Time(a.OpenedAt)
	d.Time(a.LastExceededAt)
	d.Time(a.ExpiresAt)
	d.Str(a.Title)
	d.Str(a.Summary)
	for _, v := range a.DischargeThresholds {
		d.F64(v)
	}
	for _, s := range a.RainfallThresholds {
		for _, v := range s {
			d.F64(v)
		}
	}
	d.Int(int64(len(a.Days)))
	for _, day := range a.Days {
		d.Time(day.Date)
		d.Int(int64(day.Level))
		d.Bool(day.Possible)
		d.Bool(day.FromEnsemble)
		d.Int(int64(day.Window))
		for _, v := range []*float64{day.Discharge, day.P75, day.Max, day.Rain[0], day.Rain[1], day.Rain[2]} {
			d.Bool(v != nil)
			if v != nil {
				d.F64(*v)
			}
		}
	}
	return d.Sum()
}
