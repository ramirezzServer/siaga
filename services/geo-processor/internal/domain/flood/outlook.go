package flood

import (
	"fmt"
	"math"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/series"
)

const day = 24 * time.Hour

// Day adalah penilaian satu tanggal UTC di jendela prakiraan.
type Day struct {
	// Date adalah tanggal UTC pukul 00.00.
	Date  time.Time
	Level hazard.Level
	// Possible: tingkat hari ini dinaikkan satu dari P75 ensemble.
	Possible bool
	// Debit penentu tingkat (median ensemble, atau debit tunggal bila
	// ensemble kosong), P75, dan maksimum ensemble (skenario terburuk).
	Discharge, P75, Max *float64
	// FromEnsemble: Discharge adalah median ensemble.
	FromEnsemble bool
	// Rain adalah akumulasi hujan rata-rata wilayah terbesar yang berakhir di
	// hari ini, sejajar dengan Windows; nil bila tidak ada jendela utuh.
	Rain [3]*float64
	// Window adalah jendela (jam) yang menentukan tingkat indeks hujan; 0
	// untuk debit atau bila di bawah ambang.
	Window int
}

// Outlook adalah penilaian satu keluaran model di satu titik: tingkat per hari
// dari hari ini sampai Policy.Horizon hari ke depan.
type Outlook struct {
	Indicator Indicator
	SiteID    string
	SiteName  string
	River     string
	Model     string
	// Location adalah titik representatif (sel GloFAS atau titik berat sel
	// sub-DAS) dari keluaran model.
	Location series.Point
	// FetchedAt adalah waktu ingest menerima keluaran ini (juga waktu terbitnya
	// untuk Open-Meteo).
	FetchedAt time.Time
	Days      []Day
	// Level adalah tingkat tertinggi di jendela (None bila di bawah Info).
	Level hazard.Level
	// Possible: setiap hari yang mencapai Level hanya mencapainya lewat P75.
	Possible bool
	// Peak adalah hari pertama dengan Level; nol bila Level None.
	Peak     time.Time
	MaxLevel hazard.Level
	// Ambang yang dipakai: Discharge untuk debit, Rainfall untuk indeks hujan.
	DischargeThresholds Set
	RainfallThresholds  [3]Set
}

// window mengembalikan tanggal UTC hari ini sampai +Horizon dari waktu ambil.
func (p Policy) window(fetched time.Time) []time.Time {
	today := fetched.UTC().Truncate(day)
	out := make([]time.Time, p.Horizon+1)
	for k := range out {
		out[k] = today.Add(time.Duration(k) * day)
	}
	return out
}

// AssessDischarge menilai satu keluaran debit (ADR 0020 butir 5): tingkat
// dasar dari median ensemble; bila P75 ensemble mencapai tingkat lebih tinggi,
// naik paling banyak satu ("kemungkinan"); maksimum ensemble hanya
// ditampilkan; tanpa ensemble dipakai debit tunggal tanpa kenaikan dari P75.
// run harus sudah lolos Validate.
func (p Policy) AssessDischarge(run series.DischargeRun, th DischargeThreshold) (Outlook, error) {
	if run.Site.ID != th.SiteID {
		return Outlook{}, fmt.Errorf("%w: deret %s dinilai dengan ambang %s", ErrInvalid, run.Site.ID, th.SiteID)
	}
	set := th.Effective()
	o := Outlook{
		Indicator: Discharge, SiteID: th.SiteID, SiteName: th.Name, River: th.River, Model: run.Model,
		Location: run.Cell, FetchedAt: run.FetchedAt, MaxLevel: th.MaxLevel, DischargeThresholds: set,
	}
	steps := map[time.Time]series.DischargeStep{}
	for _, s := range run.Steps {
		steps[s.ValidDate.UTC()] = s
	}
	for _, date := range p.window(run.FetchedAt) {
		d := Day{Date: date}
		if s, ok := steps[date]; ok {
			d.Discharge, d.FromEnsemble = s.Median, s.Median != nil
			if d.Discharge == nil {
				d.Discharge = s.Discharge
			}
			d.P75, d.Max = s.P75, s.Max
			if d.Discharge != nil {
				base := min(set.Level(*d.Discharge), th.MaxLevel)
				d.Level = base
				if d.FromEnsemble && d.P75 != nil && set.Level(*d.P75) > base {
					d.Level = min(base+1, th.MaxLevel)
					d.Possible = d.Level > base
				}
			}
		}
		o.Days = append(o.Days, d)
	}
	o.summarize()
	return o, nil
}

// AssessRainfall menilai satu keluaran hujan rata-rata wilayah sub-DAS: untuk
// setiap hari UTC, akumulasi 3, 6, dan 24 jam terbesar yang berakhir di hari
// itu dibandingkan ambang jendelanya; tingkat hari adalah yang tertinggi,
// dibatasi MaxLevel (Siaga). Jendela yang jamnya tidak lengkap tidak
// dihitung, sama dengan kalibrasi (ADR 0020 butir 7 dan 9).
func (p Policy) AssessRainfall(run series.WeatherRun, th RainfallThreshold) (Outlook, error) {
	if run.Site.ID != th.SiteID {
		return Outlook{}, fmt.Errorf("%w: deret %s dinilai dengan ambang %s", ErrInvalid, run.Site.ID, th.SiteID)
	}
	o := Outlook{
		Indicator: Rainfall, SiteID: th.SiteID, SiteName: th.Name, Model: run.Model,
		Location: run.Cell, FetchedAt: run.FetchedAt, MaxLevel: th.MaxLevel, RainfallThresholds: th.Windows,
	}
	hourly := map[time.Time]float64{}
	for _, s := range run.Steps {
		if s.PrecipitationMM != nil && s.ValidTime.Equal(s.ValidTime.Truncate(time.Hour)) {
			hourly[s.ValidTime.UTC()] = *s.PrecipitationMM
		}
	}
	for _, date := range p.window(run.FetchedAt) {
		d := Day{Date: date}
		for w, hours := range Windows {
			v, ok := DayMaxSum(hourly, date, hours)
			if !ok {
				continue
			}
			d.Rain[w] = &v
			// Jendela lebih panjang menang bila tingkatnya sama: 24 jam lebih
			// mewakili banjir sub-DAS daripada hujan singkat.
			if l := min(th.Windows[w].Level(v), th.MaxLevel); l != None && l >= d.Level {
				d.Level, d.Window = l, hours
			}
		}
		o.Days = append(o.Days, d)
	}
	o.summarize()
	return o, nil
}

// DayMaxSum mengembalikan akumulasi terbesar dari jendela `hours` jam
// berturut-turut (nilai per jam berstempel akhir jamnya) yang berakhir di
// tanggal UTC date, yaitu berakhir pukul 00.00 sampai 23.00 hari itu. Jendela
// yang salah satu jamnya tidak ada dilewati; ok false bila tidak ada jendela
// utuh. Definisinya sama dengan threshold.DailyMaxSum ingest yang dipakai
// menghitung ambang, termasuk pembulatan 1e-9.
func DayMaxSum(hourly map[time.Time]float64, date time.Time, hours int) (float64, bool) {
	best, ok := 0.0, false
	if hours <= 0 {
		return 0, false
	}
	for h := range 24 {
		end := date.Add(time.Duration(h) * time.Hour)
		sum, full := 0.0, true
		for k := range hours {
			v, present := hourly[end.Add(-time.Duration(k)*time.Hour)]
			if !present || math.IsNaN(v) {
				full = false
				break
			}
			sum += v
		}
		if !full {
			continue
		}
		sum = math.Round(sum*1e9) / 1e9
		if !ok || sum > best {
			best, ok = sum, true
		}
	}
	return best, ok
}

// summarize mengisi Level, Possible, dan Peak dari Days.
func (o *Outlook) summarize() {
	o.Level, o.Possible, o.Peak = None, false, time.Time{}
	for _, d := range o.Days {
		switch {
		case d.Level > o.Level:
			o.Level, o.Possible, o.Peak = d.Level, d.Possible, d.Date
		case d.Level == o.Level && d.Level != None && !d.Possible:
			o.Possible = false
		}
	}
}
