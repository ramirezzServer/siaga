package ports

import (
	"context"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/flood"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
)

// FloodSite adalah keadaan satu titik pantau banjir.
type FloodSite struct {
	// LastFetchedAt adalah waktu terima keluaran model terakhir yang dinilai.
	LastFetchedAt time.Time
	// EventID adalah kejadian terakhir titik ini (aktif atau sudah berakhir);
	// kosong bila belum pernah ada.
	EventID hazard.EventID
}

// FloodEpisode adalah ringkasan kejadian banjir tersimpan.
type FloodEpisode struct {
	StoredEvent
	OpenedAt       time.Time
	LastExceededAt time.Time
	ExpiresAt      time.Time
}

// FloodEventRecord adalah isi lengkap kejadian banjir yang ditulis ke penyimpanan.
type FloodEventRecord struct {
	flood.Assessment
	Revision int
	Digest   [32]byte
}

// FloodTx adalah operasi penyimpanan banjir di dalam satu transaksi.
type FloodTx interface {
	// LockFlood mengunci pemrosesan banjir sampai transaksi selesai, supaya
	// dua keluaran untuk titik yang sama tidak diproses bersamaan.
	LockFlood(ctx context.Context) error
	// Site mengembalikan keadaan titik; ok false bila belum pernah dinilai.
	Site(ctx context.Context, siteID string) (st FloodSite, ok bool, err error)
	// SaveSite mencatat keluaran terakhir yang dinilai dan kejadian titik.
	SaveSite(ctx context.Context, siteID string, fetchedAt time.Time, event hazard.EventID) error
	// Episode mengembalikan ringkasan kejadian; ok false bila tidak ada.
	Episode(ctx context.Context, id hazard.EventID) (ep FloodEpisode, ok bool, err error)
	// SaveEvent menyisipkan (status active) atau memperbarui kejadian, detail
	// banjir, dan penilaian per harinya. Status kejadian yang ada tidak diubah.
	SaveEvent(ctx context.Context, rec FloodEventRecord) error
	// EndEvent menandai kejadian tidak aktif.
	EndEvent(ctx context.Context, id hazard.EventID, status EventStatus, mergedInto hazard.EventID, at time.Time, revision int) error
	// DueForExpiry mengembalikan kejadian banjir aktif yang masa aktifnya
	// habis pada now, dikunci untuk transaksi ini.
	DueForExpiry(ctx context.Context, now time.Time, limit int) ([]StoredEvent, error)
	// Enqueue menulis pesan ke outbox dalam transaksi yang sama.
	Enqueue(ctx context.Context, msg OutboxMessage) error
}

// ThresholdSync merangkum penyelarasan ambang di schema ref.
type ThresholdSync struct {
	Inserted, Updated, Deleted, Unchanged int
}

// FloodStore membuka transaksi penyimpanan banjir dan menyelaraskan ambang.
type FloodStore interface {
	InTx(ctx context.Context, fn func(FloodTx) error) error
	// SyncThresholds menulis semua ambang kalibrasi ke ref.discharge_threshold
	// dan ref.rainfall_threshold, dan menghapus titik yang tidak ada lagi,
	// dalam satu transaksi.
	SyncThresholds(ctx context.Context, cal flood.Calibration) (ThresholdSync, error)
}

// FloodEncoder membentuk pesan hazard.flood.* dari keadaan domain.
type FloodEncoder interface {
	Created(a flood.Assessment, revision int) (OutboxMessage, error)
	Updated(a flood.Assessment, revision int, previous hazard.Level) (OutboxMessage, error)
	Expired(id hazard.EventID, at time.Time, reason ExpiryReason, mergedInto hazard.EventID, revision int) (OutboxMessage, error)
}
