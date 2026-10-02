// Package archivebackup adalah use case cadangan dan retensi arsip payload
// mentah (ADR 0022):
//
//   - Backup mengemas objek arsip panas per konektor per hari UTC yang sudah
//     ditutup menjadi bundel tar.zst di penyimpanan cadangan (domain
//     archivebundle), membaca ulang bundel sebelum menulis indeksnya.
//   - Verify mengunduh bundel dan memeriksa setiap payload terhadap indeks dan
//     kunci arsipnya.
//   - Restore mengembalikan isi bundel ke arsip (Garage atau folder) dengan
//     kunci yang sama, misal sebelum replay hari lama.
//   - Prune menghapus objek arsip panas yang kedaluwarsa menurut aturan
//     retensi, hanya bila objek itu tercatat di indeks cadangan.
package archivebackup

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/archivebundle"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/archivekey"
)

const (
	// windowSize adalah jendela zstd. Payload yang mirip berurutan (feed USGS
	// 2 hari berisi kejadian yang sama di setiap payload, prakiraan desa
	// bertetangga) baru terlihat dengan jendela jauh di atas 32 KiB gzip;
	// 32 MiB memberi hampir semua manfaatnya dengan memori ±120 MiB
	// (diukur dengan arsip 1 Okt 2026, ADR 0022).
	windowSize = 32 << 20
	// maxIndexBytes membatasi indeks hasil dekompresi.
	maxIndexBytes = 64 << 20
	// maxDecoderMemory membatasi memori dekompresi bundel.
	maxDecoderMemory = 1 << 30
)

// ErrCorrupt menandai bundel atau indeks cadangan yang rusak.
var ErrCorrupt = errors.New("cadangan arsip rusak")

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func newEncoder(w io.Writer) (*zstd.Encoder, error) {
	return zstd.NewWriter(w,
		zstd.WithEncoderLevel(zstd.SpeedBestCompression),
		zstd.WithWindowSize(windowSize),
		zstd.WithEncoderConcurrency(1),
		zstd.WithEncoderCRC(true),
	)
}

func newDecoder(r io.Reader) (*zstd.Decoder, error) {
	return zstd.NewReader(r,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(maxDecoderMemory),
		zstd.WithDecoderMaxWindow(64<<20),
	)
}

// bundleWriter menulis satu bagian bundel: tar berisi payload mentah
// (kunci arsip tanpa .gz) yang dikompres zstd.
type bundleWriter struct {
	buf     bytes.Buffer
	zw      *zstd.Encoder
	tw      *tar.Writer
	entries []archivebundle.Entry
	payload int64
}

func newBundleWriter() (*bundleWriter, error) {
	b := &bundleWriter{}
	zw, err := newEncoder(&b.buf)
	if err != nil {
		return nil, fmt.Errorf("encoder zstd: %w", err)
	}
	b.zw = zw
	b.tw = tar.NewWriter(zw)
	return b, nil
}

// add menambahkan satu payload; k dan sum adalah hasil emit.Unarchive.
func (b *bundleWriter) add(key string, k archivekey.Key, body []byte, sum string) error {
	e := archivebundle.Entry{Key: key, Bytes: int64(len(body)), SHA256: sum}
	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     e.Member(),
		Mode:     0o644,
		Size:     e.Bytes,
		ModTime:  k.FetchedAt,
		Format:   tar.FormatPAX,
	}
	if err := b.tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("tar %s: %w", key, err)
	}
	if _, err := b.tw.Write(body); err != nil {
		return fmt.Errorf("tar %s: %w", key, err)
	}
	b.entries = append(b.entries, e)
	b.payload += e.Bytes
	return nil
}

// compressed adalah ukuran bundel terkompres sejauh ini (blok zstd ditulis
// bertahap, jadi angka ini tertinggal paling banyak satu blok).
func (b *bundleWriter) compressed() int { return b.buf.Len() }

// finish menutup tar dan zstd lalu mengembalikan isi bundel.
func (b *bundleWriter) finish() ([]byte, error) {
	if err := b.tw.Close(); err != nil {
		return nil, fmt.Errorf("menutup tar: %w", err)
	}
	if err := b.zw.Close(); err != nil {
		return nil, fmt.Errorf("menutup zstd: %w", err)
	}
	return b.buf.Bytes(), nil
}

// readBundle memeriksa bundel terhadap indeksnya dan memanggil fn untuk
// setiap payload sesuai urutan indeks. Bundel yang tidak cocok dengan indeks
// (ukuran, SHA-256, nama/ukuran/SHA-256 entri, entri lebih atau kurang)
// menghasilkan ErrCorrupt.
func readBundle(data []byte, ix archivebundle.Index, fn func(e archivebundle.Entry, payload []byte) error) error {
	name := fmt.Sprintf("%s %s bagian %d", ix.Connector, ix.Day, ix.Part)
	if int64(len(data)) != ix.BundleBytes || sha256Hex(data) != ix.BundleSHA256 {
		return fmt.Errorf("%w: %s: ukuran atau SHA-256 bundel tidak cocok dengan indeks", ErrCorrupt, name)
	}
	zr, err := newDecoder(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%w: %s: zstd: %w", ErrCorrupt, name, err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for _, e := range ix.Entries {
		hdr, err := tr.Next()
		if err != nil {
			return fmt.Errorf("%w: %s: entri %s: %w", ErrCorrupt, name, e.Member(), err)
		}
		if hdr.Name != e.Member() || hdr.Size != e.Bytes || hdr.Typeflag != tar.TypeReg {
			return fmt.Errorf("%w: %s: entri %q (%d byte) tidak cocok dengan indeks %q (%d byte)", ErrCorrupt, name, hdr.Name, hdr.Size, e.Member(), e.Bytes)
		}
		payload, err := io.ReadAll(io.LimitReader(tr, e.Bytes+1))
		if err != nil {
			return fmt.Errorf("%w: %s: membaca %s: %w", ErrCorrupt, name, e.Member(), err)
		}
		if int64(len(payload)) != e.Bytes || sha256Hex(payload) != e.SHA256 {
			return fmt.Errorf("%w: %s: SHA-256 %s tidak cocok", ErrCorrupt, name, e.Member())
		}
		if err := fn(e, payload); err != nil {
			return err
		}
	}
	switch hdr, err := tr.Next(); {
	case errors.Is(err, io.EOF):
		return nil
	case err != nil:
		return fmt.Errorf("%w: %s: akhir tar: %w", ErrCorrupt, name, err)
	default:
		return fmt.Errorf("%w: %s: entri di luar indeks %q", ErrCorrupt, name, hdr.Name)
	}
}

func encodeIndex(ix archivebundle.Index) ([]byte, error) {
	raw, err := json.Marshal(ix)
	if err != nil {
		return nil, fmt.Errorf("indeks: %w", err)
	}
	var buf bytes.Buffer
	zw, err := newEncoder(&buf)
	if err != nil {
		return nil, fmt.Errorf("encoder zstd: %w", err)
	}
	if _, err := zw.Write(raw); err != nil {
		return nil, fmt.Errorf("indeks: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("indeks: %w", err)
	}
	return buf.Bytes(), nil
}

// decodeIndex membaca indeks dan memastikan isinya cocok dengan namanya.
func decodeIndex(data []byte, name archivebundle.Name) (archivebundle.Index, error) {
	var ix archivebundle.Index
	zr, err := newDecoder(bytes.NewReader(data))
	if err != nil {
		return ix, fmt.Errorf("%w: indeks %s: %w", ErrCorrupt, name, err)
	}
	defer zr.Close()
	raw, err := io.ReadAll(io.LimitReader(zr, maxIndexBytes+1))
	if err != nil {
		return ix, fmt.Errorf("%w: indeks %s: %w", ErrCorrupt, name, err)
	}
	if len(raw) > maxIndexBytes {
		return ix, fmt.Errorf("%w: indeks %s lebih dari %d byte", ErrCorrupt, name, maxIndexBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ix); err != nil {
		return ix, fmt.Errorf("%w: indeks %s: %w", ErrCorrupt, name, err)
	}
	if err := ix.Validate(name); err != nil {
		return ix, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	return ix, nil
}

func dayString(t time.Time) string { return archivebundle.Day(t).Format(time.DateOnly) }
