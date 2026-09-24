package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres/seriessql"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// SeriesStore adalah ports.SeriesStore di atas pool pgx.
type SeriesStore struct {
	pool *pgxpool.Pool
}

var _ ports.SeriesStore = (*SeriesStore)(nil)

// NewSeriesStore membungkus pool yang sudah terbuka. Pemanggil yang menutup pool.
func NewSeriesStore(pool *pgxpool.Pool) *SeriesStore { return &SeriesStore{pool: pool} }

// SaveWeather menyimpan satu keluaran prakiraan cuaca.
func (s *SeriesStore) SaveWeather(ctx context.Context, run series.WeatherRun) (ports.SeriesResult, error) {
	n := len(run.Steps)
	times := make([]time.Time, n)
	temp, hum, precip, cloud := make([]*float32, n), make([]*float32, n), make([]*float32, n), make([]*float32, n)
	wind, dir, gust, pres, blh, vis := make([]*float32, n), make([]*float32, n), make([]*float32, n), make([]*float32, n), make([]*float32, n), make([]*float32, n)
	code := make([]*int16, n)
	for i, st := range run.Steps {
		times[i] = st.ValidTime
		temp[i], hum[i], precip[i], cloud[i] = f32(st.TemperatureC), f32(st.HumidityPct), f32(st.PrecipitationMM), f32(st.CloudCoverPct)
		wind[i], dir[i], gust[i] = f32(st.WindSpeedKmh), f32(st.WindFromDeg), f32(st.WindGustKmh)
		pres[i], blh[i], vis[i] = f32(st.PressureHPa), f32(st.BoundaryLayerM), f32(st.VisibilityM)
		if st.WeatherCode != nil {
			c := int16(min(max(*st.WeatherCode, 0), 999))
			code[i] = &c
		}
	}
	return s.save(ctx, run.Run, seriessql.UpsertWeather,
		times, temp, hum, precip, code, cloud, wind, dir, gust, pres, blh, vis)
}

// SaveAirQuality menyimpan satu keluaran prakiraan kualitas udara.
func (s *SeriesStore) SaveAirQuality(ctx context.Context, run series.AirQualityRun) (ports.SeriesResult, error) {
	n := len(run.Steps)
	times := make([]time.Time, n)
	cols := make([][]*float32, 8)
	for c := range cols {
		cols[c] = make([]*float32, n)
	}
	for i, st := range run.Steps {
		times[i] = st.ValidTime
		for c, v := range []*float64{st.PM25, st.PM10, st.CO, st.NO2, st.SO2, st.O3, st.AerosolOpticalDepth, st.Dust} {
			cols[c][i] = f32(v)
		}
	}
	return s.save(ctx, run.Run, seriessql.UpsertAirQuality,
		times, cols[0], cols[1], cols[2], cols[3], cols[4], cols[5], cols[6], cols[7])
}

// SaveDischarge menyimpan satu keluaran prakiraan debit sungai.
func (s *SeriesStore) SaveDischarge(ctx context.Context, run series.DischargeRun) (ports.SeriesResult, error) {
	n := len(run.Steps)
	times := make([]time.Time, n)
	cols := make([][]*float32, 7)
	for c := range cols {
		cols[c] = make([]*float32, n)
	}
	for i, st := range run.Steps {
		times[i] = st.ValidDate
		for c, v := range []*float64{st.Discharge, st.Mean, st.Median, st.Max, st.Min, st.P25, st.P75} {
			cols[c][i] = f32(v)
		}
	}
	return s.save(ctx, run.Run, seriessql.UpsertDischarge,
		times, cols[0], cols[1], cols[2], cols[3], cols[4], cols[5], cols[6])
}

// save menulis titik, deret, dan langkah dalam satu transaksi. rows adalah
// array sejajar untuk parameter $4 dan seterusnya dari query langkah.
func (s *SeriesStore) save(ctx context.Context, run series.Run, upsertRows string, rows ...any) (res ports.SeriesResult, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, fmt.Errorf("memulai transaksi: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, rollback(ctx, tx))
		}
	}()
	if err = upsertSite(ctx, tx, run); err != nil {
		return res, err
	}
	if _, err = tx.Exec(ctx, seriessql.UpsertSeries,
		run.Site.ID, string(run.Dataset), run.Model, string(run.Source), run.Cell.Lat, run.Cell.Lon,
		run.Elevation, run.IssuedAt, run.FetchedAt, run.ArchiveKey,
	); err != nil {
		return res, fmt.Errorf("menyimpan deret %s %s: %w", run.Dataset, run.Site.ID, err)
	}
	args := append([]any{run.Site.ID, run.Model, run.IssuedAt}, rows...)
	if err = tx.QueryRow(ctx, upsertRows, args...).Scan(&res.Inserted, &res.Updated); err != nil {
		return res, fmt.Errorf("menyimpan langkah %s %s: %w", run.Dataset, run.Site.ID, err)
	}
	if err = tx.Commit(ctx); err != nil {
		return res, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}

func upsertSite(ctx context.Context, tx pgx.Tx, run series.Run) error {
	var region *string
	if run.Site.RegionCode != "" {
		region = &run.Site.RegionCode
	}
	if _, err := tx.Exec(ctx, seriessql.UpsertSite,
		run.Site.ID, string(run.Site.Kind), run.Site.Name, run.Site.River,
		run.Site.Location.Lat, run.Site.Location.Lon, region, run.FetchedAt,
	); err != nil {
		return fmt.Errorf("menyimpan titik %s: %w", run.Site.ID, err)
	}
	return nil
}

// f32 mengubah nilai ke presisi kolom real.
func f32(v *float64) *float32 {
	if v == nil {
		return nil
	}
	x := float32(*v)
	return &x
}
