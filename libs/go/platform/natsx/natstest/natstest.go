// Package natstest menjalankan server NATS JetStream di dalam proses untuk test,
// tanpa Docker. Hanya untuk dipakai dari file _test.go.
package natstest

import (
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

// RunServer menjalankan server JetStream di port acak dengan penyimpanan di
// folder sementara test, dan mematikannya saat test selesai. Mengembalikan URL klien.
func RunServer(tb testing.TB) string {
	tb.Helper()
	opts := &server.Options{
		Host:      "127.0.0.1",
		Port:      server.RANDOM_PORT,
		JetStream: true,
		StoreDir:  tb.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	}
	s, err := server.NewServer(opts)
	if err != nil {
		tb.Fatalf("natstest: membuat server: %v", err)
	}
	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		s.Shutdown()
		tb.Fatal("natstest: server tidak siap dalam 10 detik")
	}
	tb.Cleanup(func() {
		s.Shutdown()
		s.WaitForShutdown()
	})
	return s.ClientURL()
}
