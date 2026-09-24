package ports

import (
	"context"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hotspot"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/series"
)

// SeriesResult adalah jumlah baris deret waktu yang berubah.
type SeriesResult struct {
	// Inserted adalah baris baru; Updated adalah baris yang isinya berubah.
	// Keduanya nol untuk pesan ulangan.
	Inserted int
	Updated  int
}

// SeriesStore menyimpan deret waktu di schema ts. Setiap Save atomik:
// titik, deret, dan semua langkah tersimpan dalam satu transaksi, dan aman
// diulang untuk isi yang sama (idempotent).
type SeriesStore interface {
	SaveWeather(ctx context.Context, run series.WeatherRun) (SeriesResult, error)
	SaveAirQuality(ctx context.Context, run series.AirQualityRun) (SeriesResult, error)
	SaveDischarge(ctx context.Context, run series.DischargeRun) (SeriesResult, error)
	// SaveObservation menyimpan stasiun, deret, dan nilai sensornya.
	SaveObservation(ctx context.Context, obs series.StationObservation) (SeriesResult, error)
	// SaveHotspot menyimpan satu deteksi titik panas (tanpa titik dan deret).
	SaveHotspot(ctx context.Context, d hotspot.Detection) (SeriesResult, error)
}
