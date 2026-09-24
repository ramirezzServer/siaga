// Package timeseries adalah use case penyimpanan deret waktu geo-processor:
// prakiraan cuaca (BMKG dan Open-Meteo), prakiraan kualitas udara model,
// debit sungai, pengukuran stasiun kualitas udara, dan titik panas dari event
// raw ke hypertable schema ts. Setiap keluaran model disimpan utuh per waktu
// terbit; pesan ulangan tidak mengubah apa pun.
package timeseries

import (
	"context"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hotspot"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// Service memvalidasi lalu menyimpan deret waktu.
type Service struct {
	store ports.SeriesStore
	now   func() time.Time
}

// New membuat Service. now dipakai untuk menolak waktu ambil di masa depan.
func New(store ports.SeriesStore, now func() time.Time) *Service {
	return &Service{store: store, now: now}
}

// Result merangkum dampak satu pesan.
type Result struct {
	// Changed false berarti pesan ulangan yang tidak mengubah apa pun.
	Changed bool
	// Rows adalah baris baru ditambah baris yang isinya berubah.
	Rows int
}

func result(r ports.SeriesResult) Result {
	n := r.Inserted + r.Updated
	return Result{Changed: n > 0, Rows: n}
}

// Weather menyimpan satu keluaran prakiraan cuaca. Galat yang membungkus
// series.ErrInvalid tidak akan berhasil bila diulang.
func (s *Service) Weather(ctx context.Context, run series.WeatherRun) (Result, error) {
	if err := run.Validate(s.now()); err != nil {
		return Result{}, err
	}
	r, err := s.store.SaveWeather(ctx, run)
	return result(r), err
}

// AirQuality menyimpan satu keluaran prakiraan kualitas udara.
func (s *Service) AirQuality(ctx context.Context, run series.AirQualityRun) (Result, error) {
	if err := run.Validate(s.now()); err != nil {
		return Result{}, err
	}
	r, err := s.store.SaveAirQuality(ctx, run)
	return result(r), err
}

// Discharge menyimpan satu keluaran prakiraan debit sungai.
func (s *Service) Discharge(ctx context.Context, run series.DischargeRun) (Result, error) {
	if err := run.Validate(s.now()); err != nil {
		return Result{}, err
	}
	r, err := s.store.SaveDischarge(ctx, run)
	return result(r), err
}

// Observation menyimpan nilai terbaru satu stasiun kualitas udara.
func (s *Service) Observation(ctx context.Context, obs series.StationObservation) (Result, error) {
	if err := obs.Validate(s.now()); err != nil {
		return Result{}, err
	}
	r, err := s.store.SaveObservation(ctx, obs)
	return result(r), err
}

// Hotspot menyimpan satu deteksi titik panas. Galat yang membungkus
// hotspot.ErrInvalid tidak akan berhasil bila diulang.
func (s *Service) Hotspot(ctx context.Context, d hotspot.Detection) (Result, error) {
	if err := d.Validate(s.now()); err != nil {
		return Result{}, err
	}
	r, err := s.store.SaveHotspot(ctx, d)
	return result(r), err
}
