// Package httpstatus menyajikan endpoint kesehatan ingest:
// /healthz (proses hidup), /readyz (siap menerbitkan), /status (per konektor
// dan per sapuan).
package httpstatus

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/runner"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/sweep"
)

// Handler membangun mux. ready melaporkan apakah ingest bisa menerbitkan
// (misal NATS tersambung); status dan sweeps mengembalikan snapshot konektor
// polling dan sapuan.
func Handler(ready func() bool, status func() []runner.Status, sweeps func() []sweep.Status, now func() time.Time) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if !ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("belum siap\n"))
			return
		}
		_, _ = w.Write([]byte("siap\n"))
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Now        time.Time       `json:"now"`
			Ready      bool            `json:"ready"`
			Connectors []runner.Status `json:"connectors"`
			Sweeps     []sweep.Status  `json:"sweeps"`
		}{now().UTC(), ready(), status(), sweeps()})
	})
	return mux
}
