package ports

import (
	"context"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
)

// EventStatus adalah status kejadian di penyimpanan.
type EventStatus string

// Status kejadian; sama dengan constraint hazard.event.status.
const (
	StatusActive    EventStatus = "active"
	StatusExpired   EventStatus = "expired"
	StatusMerged    EventStatus = "merged"
	StatusRetracted EventStatus = "retracted"
)

// StoredEvent adalah ringkasan kejadian tersimpan yang dibutuhkan use case.
type StoredEvent struct {
	ID       quake.EventID
	Status   EventStatus
	Level    quake.Level
	Revision int
	Digest   [32]byte
}

// EventRecord adalah isi lengkap kejadian gempa yang ditulis ke penyimpanan.
type EventRecord struct {
	quake.Assessment
	Revision int
	Digest   [32]byte
}

// QuakeTx adalah operasi penyimpanan gempa di dalam satu transaksi.
type QuakeTx interface {
	// LockQuakes mengunci pengelompokan gempa sampai transaksi selesai, supaya
	// dua pesan yang diproses bersamaan tidak membuat kejadian ganda.
	LockQuakes(ctx context.Context) error

	// Report mengembalikan laporan tersimpan, atau nil bila belum ada.
	Report(ctx context.Context, key quake.ReportKey) (*quake.Stored, error)
	// SaveReport menulis laporan tanpa mengubah keanggotaan kejadiannya.
	SaveReport(ctx context.Context, r quake.Stored) error
	// IdentityReports mengembalikan laporan semua feed untuk satu kejadian sumber.
	IdentityReports(ctx context.Context, key quake.IdentityKey) ([]quake.Stored, error)
	// EventOf mengembalikan kejadian tempat laporan tergabung (kosong bila tidak ada).
	EventOf(ctx context.Context, key quake.IdentityKey) (quake.EventID, error)
	// Clusters mengembalikan laporan semua anggota kejadian aktif atau
	// kedaluwarsa yang punya laporan dengan waktu kejadian di [from, to],
	// ditambah kejadian di include, dikelompokkan per kejadian.
	Clusters(ctx context.Context, from, to time.Time, include []quake.EventID) (map[quake.EventID][]quake.Stored, error)
	// EventExists melaporkan apakah ID sudah pernah dipakai (status apa pun).
	EventExists(ctx context.Context, id quake.EventID) (bool, error)
	// Event mengembalikan ringkasan kejadian; ok false bila tidak ada.
	Event(ctx context.Context, id quake.EventID) (ev StoredEvent, ok bool, err error)
	// RegionsWithin mengembalikan kelurahan/desa yang batasnya berjarak paling
	// jauh radiusKm dari titik, terdekat lebih dulu.
	RegionsWithin(ctx context.Context, lat, lon, radiusKm float64) ([]quake.RegionDistance, error)

	// SetMembership menggabungkan semua laporan milik identitas ke kejadian id.
	SetMembership(ctx context.Context, id quake.EventID, members []quake.IdentityKey) error
	// Detach melepas laporan identitas dari kejadian mana pun.
	Detach(ctx context.Context, key quake.IdentityKey) error
	// SaveEvent menyisipkan atau memperbarui kejadian, detail gempa, dan
	// wilayah terdampaknya. Status kejadian yang sudah ada tidak diubah.
	SaveEvent(ctx context.Context, rec EventRecord) error
	// EndEvent menandai kejadian tidak aktif (expired, merged, retracted).
	EndEvent(ctx context.Context, id quake.EventID, status EventStatus, mergedInto quake.EventID, at time.Time, revision int) error
	// DueForExpiry mengembalikan kejadian gempa aktif yang masa aktifnya habis
	// pada now, dikunci untuk transaksi ini.
	DueForExpiry(ctx context.Context, now time.Time, limit int) ([]StoredEvent, error)
	// Enqueue menulis pesan ke outbox dalam transaksi yang sama.
	Enqueue(ctx context.Context, msg OutboxMessage) error
}

// QuakeStore membuka transaksi penyimpanan gempa.
type QuakeStore interface {
	// InTx menjalankan fn dalam satu transaksi; commit bila fn berhasil,
	// rollback bila gagal.
	InTx(ctx context.Context, fn func(QuakeTx) error) error
}

// OutboxMessage adalah satu pesan hazard.* yang menunggu diterbitkan.
type OutboxMessage struct {
	Subject string
	// MsgID deterministik untuk deduplikasi JetStream (Nats-Msg-Id).
	MsgID   string
	Payload []byte
	// TraceParent adalah konteks trace W3C saat pesan ditulis ke outbox,
	// diisi adapter penyimpanan dari context (kosong bila tidak ada span).
	// Relay meneruskannya sebagai header supaya penerbitan hazard.* masuk
	// trace yang sama dengan pesan raw.* penyebabnya.
	TraceParent string
	// CreatedAt adalah waktu pesan ditulis ke outbox (diisi saat dibaca).
	CreatedAt time.Time
}

// ExpiryReason adalah alasan kejadian tidak aktif lagi.
type ExpiryReason int

// Alasan; sama dengan siaga.hazard.v1.ExpiryReason.
const (
	ExpiryElapsed ExpiryReason = iota + 1
	ExpiryMerged
	ExpiryRetracted
)

// HazardEncoder membentuk pesan hazard.* dari keadaan domain.
type HazardEncoder interface {
	Created(a quake.Assessment, revision int) (OutboxMessage, error)
	Updated(a quake.Assessment, revision int, previous quake.Level) (OutboxMessage, error)
	Expired(id quake.EventID, at time.Time, reason ExpiryReason, mergedInto quake.EventID, revision int) (OutboxMessage, error)
}

// Outbox membaca dan menghapus pesan yang menunggu diterbitkan.
type Outbox interface {
	// Drain mengambil paling banyak limit pesan tertua yang tidak sedang
	// dikunci proses lain, memanggil publish untuk tiap pesan berurutan, lalu
	// menghapus yang berhasil. Berhenti di pesan pertama yang gagal.
	Drain(ctx context.Context, limit int, publish func(context.Context, OutboxMessage) error) (int, error)
}

// Publisher menerbitkan pesan secara tahan lama ke broker.
type Publisher interface {
	Publish(ctx context.Context, subject, msgID string, data []byte, headers map[string]string) error
}

// CatalogEvent adalah satu gempa dari katalog historis, bahan kalibrasi.
type CatalogEvent struct {
	ID string
	quake.Origin
}
