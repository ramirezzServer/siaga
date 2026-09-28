// Package aqbackfill adalah use case pengisian ulang pengukuran stasiun
// kualitas udara (OpenAQ) untuk rentang waktu yang terlewat, misal saat
// ingest mati atau jaringan putus. Polling hanya membaca nilai terbaru, jadi
// jam yang terlewat tidak pernah terambil tanpa pengisian ulang.
//
// Daftar stasiun diambil sekali (konteks sensor dan parameter), lalu nilai
// mentah setiap sensor parameter SIAGA diminta per halaman. Setiap nilai
// terbit sebagai satu event raw.aq.openaq berisi satu sensor, dengan
// FetchMeta menunjuk payload arsip halaman asalnya. Nilai yang sudah ada di
// database ditimpa dengan nilai yang sama (upsert geo-processor), jadi
// rentang boleh tumpang tindih dengan data live dan aman diulang.
package aqbackfill

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/emit"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/stations"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/airquality"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/schedule"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Connector adalah nama konektor di FetchMeta dan kunci arsip halaman nilai.
// Daftar stasiun diarsipkan dengan nama konektor polling (ListArchive),
// sehingga replay OpenAQ juga bisa memakainya sebagai konteks.
const Connector = "openaq-jam"

// ListArchive adalah nama konektor arsip daftar stasiun (sama dengan polling).
const ListArchive = "openaq-stasiun"

// PageLimit adalah jumlah nilai per halaman (batas OpenAQ v3).
const PageLimit = 1000

// MaxPages membatasi halaman per sensor. Sensor per jam hanya butuh satu
// halaman untuk rentang maksimum; sensor per menit bisa butuh lebih.
const MaxPages = 20

// MaxAge adalah umur nilai tertua yang bisa diisi ulang, relatif terhadap
// waktu ambil: umur terbesar yang diterima geo-processor
// (series.MaxObservationAge, 7 hari) dikurangi satu jam cadangan.
const MaxAge = 7*24*time.Hour - time.Hour

// MaxAttempts adalah batas percobaan satu halaman saat sumber meminta
// melambat (HTTP 429/503) atau jaringan gagal.
const MaxAttempts = 3

// Sensor adalah satu sensor parameter SIAGA di stasiun.
type Sensor struct {
	ID        int64
	Parameter airquality.Parameter
	Unit      string
}

// Measurement adalah satu nilai mentah sensor.
type Measurement struct {
	Parameter  airquality.Parameter
	Unit       string
	Value      float64
	ObservedAt time.Time
}

// Source adalah sumber stasiun dengan riwayat nilai per sensor (dipenuhi
// adapter openaq.Source).
type Source interface {
	ListRequest() ports.Request
	ParseList(body []byte) ([]stations.Station, []ports.Rejection, error)
	// Sensors mengembalikan sensor parameter SIAGA milik st menurut daftar
	// terakhir yang dibaca ParseList.
	Sensors(st stations.Station) []Sensor
	MeasurementsRequest(sensorID int64, from, to time.Time, page int) ports.Request
	ParseMeasurements(body []byte) ([]Measurement, error)
	Event(o airquality.Observation) (ports.Event, error)
}

// Options mengatur Run.
type Options struct {
	// From dan To membatasi waktu ukur: From <= t <= To. To nol berarti sekarang.
	From, To time.Time
	// Stations membatasi ke ID stasiun ini (misal "openaq:6539694"). Kosong
	// berarti semua stasiun di daftar yang melapor pada atau setelah From.
	Stations []string
}

// ErrOptions menandai opsi yang tidak masuk akal.
var ErrOptions = errors.New("opsi pengisian ulang tidak valid")

// MaxSamples membatasi contoh penolakan per sensor.
const MaxSamples = 5

// SensorReport adalah hasil satu sensor.
type SensorReport struct {
	Station   string `json:"station"`
	Name      string `json:"name"`
	Sensor    int64  `json:"sensor"`
	Parameter string `json:"parameter"`
	Pages     int    `json:"pages"`
	// Rows adalah nilai di semua halaman; InRange yang waktu ukurnya di rentang.
	Rows       int       `json:"rows"`
	InRange    int       `json:"in_range"`
	Published  int       `json:"published"`
	Duplicates int       `json:"duplicates"`
	Rejected   int       `json:"rejected"`
	First      time.Time `json:"first,omitzero"`
	Last       time.Time `json:"last,omitzero"`
	// Error diisi bila sensor berhenti di tengah (halaman gagal diambil atau
	// dibaca); sensor lain tetap diproses.
	Error   string   `json:"error,omitempty"`
	Samples []string `json:"samples,omitempty"`
}

func (r *SensorReport) reject(err error) {
	r.Rejected++
	if len(r.Samples) < MaxSamples {
		r.Samples = append(r.Samples, err.Error())
	}
}

// Report merangkum satu pengisian ulang.
type Report struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// Stations adalah stasiun yang diproses; Silent stasiun di daftar yang
	// tidak melapor sejak From (dilewati).
	Stations   int            `json:"stations"`
	Silent     int            `json:"silent"`
	ListErrors []string       `json:"list_rejections,omitempty"`
	Sensors    []SensorReport `json:"sensors"`
}

// Totals menjumlahkan semua sensor.
func (r Report) Totals() SensorReport {
	t := SensorReport{Station: "total"}
	for _, s := range r.Sensors {
		t.Pages += s.Pages
		t.Rows += s.Rows
		t.InRange += s.InRange
		t.Published += s.Published
		t.Duplicates += s.Duplicates
		t.Rejected += s.Rejected
	}
	return t
}

// Backfiller menjalankan pengisian ulang.
type Backfiller struct {
	src      Source
	fetch    ports.Fetcher
	archive  ports.Archive // nil berarti arsip dimatikan
	pub      ports.Publisher
	clock    ports.Clock
	throttle ports.Throttle
}

// New membuat Backfiller. archive boleh nil.
func New(src Source, fetch ports.Fetcher, archive ports.Archive, pub ports.Publisher, clock ports.Clock, throttle ports.Throttle) *Backfiller {
	return &Backfiller{src: src, fetch: fetch, archive: archive, pub: pub, clock: clock, throttle: throttle}
}

// Run mengisi ulang rentang opts. Galat hanya untuk opsi, daftar stasiun,
// penerbitan, atau pembatalan; kegagalan per sensor dicatat di laporan.
func (b *Backfiller) Run(ctx context.Context, opts Options) (Report, error) {
	now := b.clock.Now().UTC()
	if opts.To.IsZero() {
		opts.To = now
	}
	rep := Report{From: opts.From.UTC(), To: opts.To.UTC()}
	if err := validate(opts, now); err != nil {
		return rep, err
	}
	body, fetchedAt, err := b.get(ctx, b.src.ListRequest())
	if err != nil {
		return rep, fmt.Errorf("daftar stasiun: %w", err)
	}
	if b.archive != nil {
		if _, err := emit.Archive(ctx, b.archive, ListArchive, "json", emit.Sum(body), body, fetchedAt); err != nil {
			return rep, err
		}
	}
	list, rejected, err := b.src.ParseList(body)
	if err != nil {
		return rep, fmt.Errorf("daftar stasiun: %w", err)
	}
	for _, r := range rejected {
		rep.ListErrors = append(rep.ListErrors, fmt.Sprintf("%s: %v", r.Key, r.Reason))
	}
	selected, silent, err := pick(list, opts)
	if err != nil {
		return rep, err
	}
	rep.Stations, rep.Silent = len(selected), silent
	for _, st := range selected {
		for _, sn := range b.src.Sensors(st) {
			sr := SensorReport{Station: st.ID, Name: st.Name, Sensor: sn.ID, Parameter: string(sn.Parameter)}
			err := b.sensor(ctx, st, sn, opts, &sr)
			rep.Sensors = append(rep.Sensors, sr)
			if err != nil {
				return rep, err
			}
		}
	}
	return rep, nil
}

func validate(opts Options, now time.Time) error {
	var errs []error
	switch {
	case opts.From.IsZero():
		errs = append(errs, errors.New("awal rentang wajib diisi"))
	case !opts.From.Before(opts.To):
		errs = append(errs, fmt.Errorf("awal %s harus sebelum akhir %s", opts.From.UTC().Format(time.RFC3339), opts.To.UTC().Format(time.RFC3339)))
	case opts.From.Before(now.Add(-MaxAge)):
		errs = append(errs, fmt.Errorf("awal %s lebih tua dari %v: geo-processor menolak nilai setua itu",
			opts.From.UTC().Format(time.RFC3339), MaxAge))
	}
	if opts.To.After(now.Add(airquality.MaxClockSkew)) {
		errs = append(errs, fmt.Errorf("akhir %s di masa depan", opts.To.UTC().Format(time.RFC3339)))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("%w: %w", ErrOptions, err)
	}
	return nil
}

// pick memilih stasiun: yang disebut opts.Stations, atau semua yang melapor
// sejak opts.From. Urut ID supaya laporan deterministik.
func pick(list []stations.Station, opts Options) (selected []stations.Station, silent int, err error) {
	slices.SortFunc(list, func(a, b stations.Station) int { return cmp.Compare(a.ID, b.ID) })
	list = slices.CompactFunc(list, func(a, b stations.Station) bool { return a.ID == b.ID })
	if len(opts.Stations) > 0 {
		var unknown []string
		for _, id := range opts.Stations {
			i := slices.IndexFunc(list, func(s stations.Station) bool { return s.ID == id })
			if i < 0 {
				unknown = append(unknown, id)
				continue
			}
			selected = append(selected, list[i])
		}
		if len(unknown) > 0 {
			return nil, 0, fmt.Errorf("%w: stasiun %v tidak ada di daftar (atau tanpa sensor parameter SIAGA)", ErrOptions, unknown)
		}
		return selected, 0, nil
	}
	for _, st := range list {
		if st.LastReport.IsZero() || st.LastReport.Before(opts.From) {
			silent++
			continue
		}
		selected = append(selected, st)
	}
	return selected, silent, nil
}

// sensor mengisi ulang satu sensor, halaman demi halaman.
func (b *Backfiller) sensor(ctx context.Context, st stations.Station, sn Sensor, opts Options, sr *SensorReport) error {
	for page := 1; ; page++ {
		if page > MaxPages {
			sr.Error = fmt.Sprintf("lebih dari %d halaman; persempit rentang", MaxPages)
			return nil
		}
		req := b.src.MeasurementsRequest(sn.ID, opts.From, opts.To, page)
		body, fetchedAt, err := b.get(ctx, req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			sr.Error = fmt.Sprintf("halaman %d: %v", page, err)
			return nil
		}
		sr.Pages++
		sum := emit.Sum(body)
		meta := ports.FetchMeta{Connector: Connector, FetchedAt: fetchedAt, PayloadSHA256: sum}
		if b.archive != nil {
			key, err := emit.Archive(ctx, b.archive, Connector, "json", sum, body, fetchedAt)
			if err != nil {
				return err
			}
			meta.ArchiveKey = key
		}
		rows, err := b.src.ParseMeasurements(body)
		if err != nil {
			sr.Error = fmt.Sprintf("halaman %d: %v", page, err)
			return nil
		}
		sr.Rows += len(rows)
		for _, m := range rows {
			if err := b.one(ctx, st, sn, m, meta, opts, sr); err != nil {
				return err
			}
		}
		if len(rows) < PageLimit {
			return nil
		}
	}
}

// one menerbitkan satu nilai. Galat hanya untuk kegagalan penerbitan.
func (b *Backfiller) one(ctx context.Context, st stations.Station, sn Sensor, m Measurement, meta ports.FetchMeta, opts Options, sr *SensorReport) error {
	if m.ObservedAt.Before(opts.From) || m.ObservedAt.After(opts.To) {
		return nil
	}
	sr.InRange++
	if m.Parameter != sn.Parameter || m.Unit != sn.Unit {
		sr.reject(fmt.Errorf("%s: parameter %s %s, daftar menyebut %s %s", m.ObservedAt.Format(time.RFC3339),
			m.Parameter, m.Unit, sn.Parameter, sn.Unit))
		return nil
	}
	r := airquality.Reading{SensorID: sn.ID, Parameter: sn.Parameter, Unit: sn.Unit, Value: m.Value, ObservedAt: m.ObservedAt}
	kept, dropped := airquality.Clean([]airquality.Reading{r}, meta.FetchedAt, MaxAge)
	for _, d := range dropped {
		sr.reject(d)
	}
	if len(kept) == 0 {
		return nil
	}
	obs := airquality.Observation{Station: st.Station, Readings: kept}
	if err := obs.Validate(meta.FetchedAt, MaxAge); err != nil {
		sr.reject(err)
		return nil
	}
	ev, err := b.src.Event(obs)
	if err != nil {
		sr.reject(err)
		return nil
	}
	content, _, err := emit.Content(ev)
	if err != nil {
		sr.reject(err)
		return nil
	}
	ack, err := emit.Publish(ctx, b.pub, ev, content, meta)
	if err != nil {
		return err
	}
	if ack.Duplicate {
		sr.Duplicates++
	} else {
		sr.Published++
	}
	if sr.First.IsZero() || m.ObservedAt.Before(sr.First) {
		sr.First = m.ObservedAt
	}
	if m.ObservedAt.After(sr.Last) {
		sr.Last = m.ObservedAt
	}
	return nil
}

// get mengambil satu request dengan izin anggaran, mencoba ulang bila sumber
// meminta melambat atau jaringan gagal. Mengembalikan isi dan waktu ambil.
func (b *Backfiller) get(ctx context.Context, req ports.Request) ([]byte, time.Time, error) {
	var last error
	for attempt := range MaxAttempts {
		if attempt > 0 {
			var after time.Duration
			if ra, ok := errors.AsType[*ports.RetryAfterError](last); ok {
				after = ra.After
			}
			if err := b.clock.Sleep(ctx, schedule.Next(10*time.Second, attempt-1, 2*time.Minute, after)); err != nil {
				return nil, time.Time{}, err
			}
		}
		if err := b.throttle.Wait(ctx); err != nil {
			return nil, time.Time{}, err
		}
		resp, err := b.fetch.Fetch(ctx, req)
		if err == nil && resp.NotModified {
			err = errors.New("sumber menjawab 304 untuk request tanpa validator")
		}
		if err == nil {
			return resp.Body, b.clock.Now().UTC(), nil
		}
		if ctx.Err() != nil {
			return nil, time.Time{}, ctx.Err()
		}
		last = fmt.Errorf("mengambil %s: %w", req.Redacted(), err)
		if sc, ok := errors.AsType[ports.StatusCoder](err); ok && sc.HTTPStatus() < 500 && sc.HTTPStatus() != 429 {
			return nil, time.Time{}, last // 4xx selain 429 tidak membaik dengan diulang
		}
	}
	return nil, time.Time{}, last
}
