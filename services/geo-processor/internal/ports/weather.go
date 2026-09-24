package ports

import (
	"context"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/weather"
)

// WeatherEventRecord adalah isi lengkap kejadian cuaca yang ditulis ke penyimpanan.
type WeatherEventRecord struct {
	weather.Assessment
	Revision int
	Digest   [32]byte
}

// WeatherTx adalah operasi penyimpanan peringatan cuaca di dalam satu transaksi.
type WeatherTx interface {
	// LockWeather mengunci pemrosesan peringatan cuaca sampai transaksi
	// selesai, supaya dua pesan dari rantai yang sama tidak diproses bersamaan.
	LockWeather(ctx context.Context) error

	// Message mengembalikan pesan tersimpan (tanpa poligon), atau nil.
	Message(ctx context.Context, key weather.Key) (*weather.Message, error)
	// SaveMessage menyisipkan atau menimpa pesan beserta rujukannya, dan
	// menyimpan areanya setelah poligon digabung dan diperbaiki. Keanggotaan
	// kejadian tidak diubah.
	SaveMessage(ctx context.Context, m weather.Message) error
	// Component mengembalikan semua pesan tersimpan yang terhubung dengan key
	// lewat references (ke dua arah, transitif), termasuk key sendiri.
	Component(ctx context.Context, key weather.Key) ([]weather.Message, error)
	// Footprint menghitung area tersimpan pesan key dan kelurahan/desa yang
	// beririsan dengannya.
	Footprint(ctx context.Context, key weather.Key) (weather.Footprint, error)

	// Event mengembalikan ringkasan kejadian; ok false bila tidak ada.
	Event(ctx context.Context, id hazard.EventID) (ev StoredEvent, ok bool, err error)
	// SaveEvent menyisipkan (status active) atau memperbarui isi kejadian,
	// detail cuaca, dan wilayah terdampaknya. Status kejadian yang sudah ada
	// tidak diubah.
	SaveEvent(ctx context.Context, rec WeatherEventRecord) error
	// Reactivate mengaktifkan lagi kejadian yang sudah berakhir.
	Reactivate(ctx context.Context, id hazard.EventID) error
	// EndEvent menandai kejadian tidak aktif (expired, merged, retracted).
	EndEvent(ctx context.Context, id hazard.EventID, status EventStatus, mergedInto hazard.EventID, at time.Time, revision int) error
	// SetMembership menautkan pesan ke kejadian id.
	SetMembership(ctx context.Context, id hazard.EventID, keys []weather.Key) error
	// DueForExpiry mengembalikan kejadian cuaca aktif yang masa berlakunya
	// habis pada now, dikunci untuk transaksi ini.
	DueForExpiry(ctx context.Context, now time.Time, limit int) ([]StoredEvent, error)
	// Enqueue menulis pesan ke outbox dalam transaksi yang sama.
	Enqueue(ctx context.Context, msg OutboxMessage) error
}

// WeatherStore membuka transaksi penyimpanan peringatan cuaca.
type WeatherStore interface {
	InTx(ctx context.Context, fn func(WeatherTx) error) error
}

// WeatherEncoder membentuk pesan hazard.weather.* dari keadaan domain.
type WeatherEncoder interface {
	Created(a weather.Assessment, revision int) (OutboxMessage, error)
	Updated(a weather.Assessment, revision int, previous hazard.Level) (OutboxMessage, error)
	Expired(id hazard.EventID, at time.Time, reason ExpiryReason, mergedInto hazard.EventID, revision int) (OutboxMessage, error)
}
