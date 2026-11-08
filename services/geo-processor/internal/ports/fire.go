package ports

import (
	"context"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
)

// FireMember adalah satu deteksi anggota kejadian titik api aktif.
type FireMember struct {
	EventID hazard.EventID
	fire.Detection
}

// FireEpisode adalah ringkasan kejadian titik api tersimpan.
type FireEpisode struct {
	StoredEvent
	OpenedBy       string
	FirstDetected  time.Time
	ExpiresAt      time.Time
	DetectionCount int
}

// FireEventRecord adalah isi lengkap kejadian titik api yang ditulis ke penyimpanan.
type FireEventRecord struct {
	fire.Assessment
	Revision int
	Digest   [32]byte
}

// FireTx adalah operasi penyimpanan titik api di dalam satu transaksi.
type FireTx interface {
	// LockFire mengunci pemrosesan titik api sampai transaksi selesai,
	// supaya dua deteksi yang berdekatan tidak membuka dua kejadian.
	LockFire(ctx context.Context) error
	// HasDetection true bila deteksi sudah menjadi anggota kejadian.
	HasDetection(ctx context.Context, id string) (bool, error)
	// Nearby mengembalikan anggota kejadian aktif yang waktu deteksinya
	// paling jauh gap dari d dan berjarak kira-kira ≤ km (saringan kasar;
	// use case memeriksa ulang dengan fire.Policy.Linked).
	Nearby(ctx context.Context, d fire.Detection, gap time.Duration, km float64) ([]FireMember, error)
	// Episode mengembalikan ringkasan kejadian; ok false bila tidak ada.
	Episode(ctx context.Context, id hazard.EventID) (ep FireEpisode, ok bool, err error)
	// Members mengembalikan semua deteksi anggota kejadian.
	Members(ctx context.Context, id hazard.EventID) ([]fire.Detection, error)
	// SaveEvent menyisipkan (status active) atau memperbarui kejadian dan
	// detail titik apinya. Status kejadian yang ada tidak diubah.
	SaveEvent(ctx context.Context, rec FireEventRecord) error
	// AddDetection mencatat deteksi sebagai anggota kejadian.
	AddDetection(ctx context.Context, event hazard.EventID, d fire.Detection) error
	// MoveDetections memindahkan semua anggota from ke to (penggabungan).
	MoveDetections(ctx context.Context, from, to hazard.EventID) error
	// EndEvent menandai kejadian tidak aktif.
	EndEvent(ctx context.Context, id hazard.EventID, status EventStatus, mergedInto hazard.EventID, at time.Time, revision int) error
	// DueForExpiry mengembalikan kejadian titik api aktif yang masa aktifnya
	// habis pada now, dikunci untuk transaksi ini.
	DueForExpiry(ctx context.Context, now time.Time, limit int) ([]StoredEvent, error)
	// Enqueue menulis pesan ke outbox dalam transaksi yang sama.
	Enqueue(ctx context.Context, msg OutboxMessage) error
}

// FireStore membuka transaksi penyimpanan titik api dan menyelaraskan
// saringan sumber panas tetap.
type FireStore interface {
	InTx(ctx context.Context, fn func(FireTx) error) error
	// SyncStatic menulis semua sumber panas tetap kalibrasi ke
	// ref.fire_static_source dan menghapus yang tidak ada lagi, dalam satu
	// transaksi.
	SyncStatic(ctx context.Context, cal fire.Calibration) (ThresholdSync, error)
}

// FireEncoder membentuk pesan hazard.fire.* dari keadaan domain.
type FireEncoder interface {
	Created(a fire.Assessment, revision int) (OutboxMessage, error)
	Updated(a fire.Assessment, revision int, previous hazard.Level) (OutboxMessage, error)
	Expired(id hazard.EventID, at time.Time, reason ExpiryReason, mergedInto hazard.EventID, revision int) (OutboxMessage, error)
}
