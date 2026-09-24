// Package natsx menyiapkan koneksi NATS dan stream JetStream yang seragam
// untuk semua layanan Go. Definisi stream ada di libs/go/contracts/streams.
package natsx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
)

// Connect membuka koneksi yang terus mencoba tersambung ulang tanpa batas,
// karena layanan lebih baik menunggu NATS pulih daripada mati.
func Connect(url, client string, log *slog.Logger) (*nats.Conn, error) {
	nc, err := nats.Connect(url,
		nats.Name(client),
		nats.Timeout(5*time.Second),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.ReconnectJitter(500*time.Millisecond, time.Second),
		nats.RetryOnFailedConnect(true),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			log.Warn("NATS terputus", slog.Any("error", err))
		}),
		nats.ReconnectHandler(func(c *nats.Conn) {
			log.Info("NATS tersambung ulang", slog.String("url", c.ConnectedUrlRedacted()))
		}),
		nats.ErrorHandler(func(_ *nats.Conn, sub *nats.Subscription, err error) {
			subject := ""
			if sub != nil {
				subject = sub.Subject
			}
			log.Error("galat NATS asinkron", slog.String("subject", subject), slog.Any("error", err))
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("natsx: koneksi ke %s: %w", url, err)
	}
	return nc, nil
}

// StreamConfig menerjemahkan Spec kontrak ke konfigurasi JetStream.
func StreamConfig(s streams.Spec) jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:        s.Name,
		Description: s.Description,
		Subjects:    s.Subjects,
		Retention:   jetstream.LimitsPolicy,
		Discard:     jetstream.DiscardOld,
		Storage:     jetstream.FileStorage,
		Replicas:    1,
		MaxAge:      s.MaxAge,
		MaxBytes:    s.MaxBytes,
		Duplicates:  s.DuplicateWindow,
	}
}

// ErrNotOwner dikembalikan bila layanan mencoba membuat stream milik layanan lain.
var ErrNotOwner = errors.New("natsx: layanan bukan pemilik stream")

// EnsureStream membuat stream milik owner, atau memperbarui konfigurasinya bila
// sudah ada. Idempotent, aman dipanggil setiap layanan start. Stream bersama
// (streams.OwnerShared) boleh dipastikan oleh layanan mana pun.
func EnsureStream(ctx context.Context, js jetstream.JetStream, s streams.Spec, owner string) (jetstream.Stream, error) {
	if s.Owner != owner && s.Owner != streams.OwnerShared {
		return nil, fmt.Errorf("%w: %s dimiliki %s, bukan %s", ErrNotOwner, s.Name, s.Owner, owner)
	}
	st, err := js.CreateOrUpdateStream(ctx, StreamConfig(s))
	if err != nil {
		return nil, fmt.Errorf("natsx: menyiapkan stream %s: %w", s.Name, err)
	}
	return st, nil
}
