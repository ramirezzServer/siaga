// Package emit berisi langkah bersama semua use case ingest: mengarsipkan
// payload mentah dengan kunci baku dan menerbitkan event dengan ID
// deterministik. Dipakai polling feed tunggal, feed CAP, sapuan prakiraan,
// serta kebalikannya (Unarchive) untuk replay dan verifikasi arsip.
package emit

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/archivekey"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/eventid"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Sum mengembalikan SHA-256 heksadesimal dari b.
func Sum(b []byte) string {
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}

// ArchiveKey membentuk kunci arsip baku (lihat domain/archivekey).
func ArchiveKey(connector, ext, sum string, at time.Time) string {
	return archivekey.Build(connector, ext, sum, at)
}

// Archive mengompres body lalu menyimpannya di arsip. sum adalah Sum(body).
func Archive(ctx context.Context, a ports.Archive, connector, ext, sum string, body []byte, at time.Time) (string, error) {
	key := ArchiveKey(connector, ext, sum, at)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(body); err != nil {
		return "", fmt.Errorf("kompresi arsip: %w", err)
	}
	if err := zw.Close(); err != nil {
		return "", fmt.Errorf("kompresi arsip: %w", err)
	}
	if err := a.Put(ctx, key, buf.Bytes()); err != nil {
		return "", fmt.Errorf("arsip %s: %w", key, err)
	}
	return key, nil
}

// Content mengembalikan isi event tanpa meta beserta SHA-256-nya, untuk
// mendeteksi record yang sudah pernah terbit.
func Content(ev ports.Event) (content []byte, sum string, err error) {
	content, err = ev.Content()
	if err != nil {
		return nil, "", fmt.Errorf("serialisasi record %s: %w", ev.Key(), err)
	}
	return content, Sum(content), nil
}

// Publish menerbitkan event dengan Nats-Msg-Id dari (konektor, kunci, isi).
// content adalah hasil Content untuk event yang sama.
func Publish(ctx context.Context, pub ports.Publisher, ev ports.Event, content []byte, meta ports.FetchMeta) (ports.PublishResult, error) {
	data, err := ev.Encode(meta)
	if err != nil {
		return ports.PublishResult{}, fmt.Errorf("encode record %s: %w", ev.Key(), err)
	}
	msg := ports.Message{
		Subject: ev.Subject(), ID: eventid.MsgID(meta.Connector, ev.Key(), content), Data: data,
		FetchedAt: meta.FetchedAt,
	}
	res, err := pub.Publish(ctx, msg)
	if err != nil {
		return res, fmt.Errorf("menerbitkan %s ke %s: %w", ev.Key(), msg.Subject, err)
	}
	return res, nil
}

// ErrCorrupt menandai objek arsip yang tidak bisa dipercaya: kunci tidak
// baku, gzip rusak, terlalu besar, atau SHA-256 isinya tidak cocok dengan kunci.
var ErrCorrupt = errors.New("objek arsip rusak")

// DefaultMaxPayload membatasi payload hasil dekompresi (bom gzip).
const DefaultMaxPayload = 256 << 20

// Unarchive adalah kebalikan Archive: membaca kunci baku, mendekompresi data,
// dan memastikan SHA-256 payload cocok dengan potongan di kunci. Mengembalikan
// payload beserta SHA-256 penuhnya.
func Unarchive(key string, data []byte, maxPayload int64) (k archivekey.Key, body []byte, sum string, err error) {
	if k, err = archivekey.Parse(key); err != nil {
		return k, nil, "", fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return k, nil, "", fmt.Errorf("%w: %s: gzip: %w", ErrCorrupt, key, err)
	}
	body, err = io.ReadAll(io.LimitReader(zr, maxPayload+1))
	if err == nil {
		err = zr.Close()
	}
	if err != nil {
		return k, nil, "", fmt.Errorf("%w: %s: gzip: %w", ErrCorrupt, key, err)
	}
	if int64(len(body)) > maxPayload {
		return k, nil, "", fmt.Errorf("%w: %s: payload lebih dari %d byte", ErrCorrupt, key, maxPayload)
	}
	sum = Sum(body)
	if !k.Matches(sum) {
		return k, nil, "", fmt.Errorf("%w: %s: SHA-256 isi %s tidak cocok dengan kunci", ErrCorrupt, key, sum[:archivekey.SumLen])
	}
	return k, body, sum, nil
}
