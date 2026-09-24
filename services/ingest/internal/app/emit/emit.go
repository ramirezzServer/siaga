// Package emit berisi langkah bersama semua use case ingest: mengarsipkan
// payload mentah dengan kunci baku dan menerbitkan event dengan ID
// deterministik. Dipakai polling feed tunggal, feed CAP, dan sapuan prakiraan.
package emit

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/eventid"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Sum mengembalikan SHA-256 heksadesimal dari b.
func Sum(b []byte) string {
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}

// ArchiveKey membentuk kunci arsip <konektor>/<YYYY>/<MM>/<DD>/<hhmmss>Z-<sha256[:12]>.<ext>.gz (UTC).
func ArchiveKey(connector, ext, sum string, at time.Time) string {
	return fmt.Sprintf("%s/%s-%s.%s.gz", connector, at.UTC().Format("2006/01/02/150405Z"), sum[:min(12, len(sum))], ext)
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
	msg := ports.Message{Subject: ev.Subject(), ID: eventid.MsgID(meta.Connector, ev.Key(), content), Data: data}
	res, err := pub.Publish(ctx, msg)
	if err != nil {
		return res, fmt.Errorf("menerbitkan %s ke %s: %w", ev.Key(), msg.Subject, err)
	}
	return res, nil
}
