// Package ports mendefinisikan batas antara use case ingest dan dunia luar:
// sumber data (HTTP), arsip payload mentah, penerbit event (NATS), dan jam.
package ports

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"time"
)

// Request adalah permintaan ke sumber data.
type Request struct {
	URL    string
	Accept string
	// MaxBytes membatasi ukuran respons; respons lebih besar dianggap galat.
	MaxBytes int64
	// Validator dari respons sebelumnya untuk conditional request (304).
	ETag         string
	LastModified string
	// Header tambahan, misal key API sumber (X-API-Key).
	Header map[string]string
	// Secrets adalah potongan URL atau header yang rahasia (misal MAP_KEY
	// FIRMS di path). Fetcher dan use case tidak boleh menuliskannya ke
	// galat atau log; pakai Redacted untuk menampilkan URL.
	Secrets []string
}

// Redacted mengembalikan URL dengan setiap rahasia diganti "***".
func (r Request) Redacted() string { return Redact(r.URL, r.Secrets) }

// Redact mengganti setiap rahasia yang tidak kosong di s dengan "***".
func Redact(s string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "***")
		}
	}
	return s
}

// Response adalah respons sumber yang sudah dibaca utuh.
type Response struct {
	NotModified  bool
	Body         []byte
	ETag         string
	LastModified string
}

// Fetcher mengambil data dari sumber.
type Fetcher interface {
	Fetch(ctx context.Context, req Request) (Response, error)
}

// RetryAfterError dikembalikan Fetcher saat sumber meminta klien melambat
// (HTTP 429 atau 503). After nol bila sumber tidak menyebut durasi.
type RetryAfterError struct {
	Status int
	After  time.Duration
}

func (e *RetryAfterError) Error() string {
	return fmt.Sprintf("sumber meminta melambat (HTTP %d, Retry-After %v)", e.Status, e.After)
}

// Archive menyimpan payload mentah. Put harus idempotent untuk key yang sama.
type Archive interface {
	Put(ctx context.Context, key string, data []byte) error
}

// ErrArchiveNotFound dikembalikan ArchiveReader.Get untuk kunci yang tidak ada.
var ErrArchiveNotFound = errors.New("objek arsip tidak ditemukan")

// ArchiveObject adalah satu entri daftar arsip.
type ArchiveObject struct {
	Key  string
	Size int64
}

// ArchiveReader membaca arsip untuk replay, verifikasi, dan penyalinan.
type ArchiveReader interface {
	// Get membaca isi objek apa adanya (payload masih ter-gzip).
	Get(ctx context.Context, key string) ([]byte, error)
	// List mengembalikan objek berawalan prefix yang kuncinya lebih besar dari
	// startAfter (kosong = dari awal), urut byte kunci seperti ListObjectsV2
	// S3. Iterasi berhenti di galat pertama.
	List(ctx context.Context, prefix, startAfter string) iter.Seq2[ArchiveObject, error]
}

// ArchiveStore adalah arsip yang bisa ditulis dan dibaca.
type ArchiveStore interface {
	Archive
	ArchiveReader
}

// Message adalah satu pesan yang siap diterbitkan.
type Message struct {
	Subject string
	// ID deterministik untuk deduplikasi di broker (Nats-Msg-Id).
	ID   string
	Data []byte
	// FetchedAt adalah waktu ambil payload sumber (sama dengan FetchMeta),
	// dikirim juga sebagai header supaya consumer bisa mengukur latensi pipa.
	FetchedAt time.Time
}

// PublishResult melaporkan hasil penerbitan.
type PublishResult struct {
	// Duplicate true bila broker sudah pernah menerima ID yang sama.
	Duplicate bool
}

// Publisher menerbitkan pesan secara tahan lama (broker mengonfirmasi simpan).
type Publisher interface {
	Publish(ctx context.Context, msg Message) (PublishResult, error)
}

// Clock menyediakan waktu dan tidur yang bisa diganti di test.
type Clock interface {
	Now() time.Time
	// Sleep menunggu d atau sampai ctx selesai; mengembalikan ctx.Err() bila batal.
	Sleep(ctx context.Context, d time.Duration) error
}

// FetchMeta adalah keterangan pengambilan yang dilekatkan ke setiap event.
type FetchMeta struct {
	Connector     string
	FetchedAt     time.Time
	ArchiveKey    string
	PayloadSHA256 string
}

// Event adalah satu record dari sumber yang siap diterbitkan.
type Event interface {
	Subject() string
	// Key adalah ID record yang stabil di sumber (misal ID gempa).
	Key() string
	// Content adalah serialisasi deterministik isi record tanpa FetchMeta,
	// dipakai untuk ID pesan dan untuk mendeteksi revisi.
	Content() ([]byte, error)
	// Encode menghasilkan isi pesan lengkap beserta FetchMeta.
	Encode(meta FetchMeta) ([]byte, error)
}

// Rejection adalah record yang ditolak validasi; record lain tetap diproses.
type Rejection struct {
	Key    string
	Reason error
}

// Connector adalah satu feed dari satu sumber.
type Connector interface {
	// Name unik dan stabil, dipakai di log, arsip, dan ID pesan (misal "bmkg-autogempa").
	Name() string
	Request() Request
	// ArchiveExt adalah ekstensi payload mentah tanpa titik (misal "json").
	ArchiveExt() string
	// Parse mengubah payload menjadi event. Galat berarti seluruh payload tidak
	// bisa dibaca (misal format sumber berubah); record yang rusak sendirian
	// dilaporkan lewat Rejection.
	Parse(body []byte, fetchedAt time.Time) ([]Event, []Rejection, error)
}

// StatusCoder dipenuhi galat Fetcher yang membawa kode status HTTP sumber
// (misal 404 untuk kode wilayah yang tidak dikenal sumber).
type StatusCoder interface {
	error
	HTTPStatus() int
}

// Throttle menahan pemanggil sampai anggaran request mengizinkan satu request.
// Dipakai use case yang mengirim lebih dari satu request per putaran (misal
// dokumen CAP per peringatan atau sapuan prakiraan per kelurahan).
type Throttle interface {
	// Wait mengembalikan ctx.Err() bila dibatalkan sebelum izin didapat.
	Wait(ctx context.Context) error
}

// FeedItem adalah satu entri daftar peringatan (misal RSS nowcast BMKG) yang
// menunjuk ke dokumen detail.
type FeedItem struct {
	// Key adalah ID pesan di sumber (RSS guid = identifier CAP).
	Key string
	// File adalah nama dokumen detail di sumber, misal "CJB20260924001_alert.xml".
	File  string
	Title string
	// Digest berubah bila isi entri berubah; dokumen detail lalu diambil ulang.
	Digest string
}
