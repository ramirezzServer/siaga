// Package httpstatus menyajikan endpoint kesehatan geo-processor:
// /healthz (proses hidup), /readyz (NATS dan database siap), /status (statistik).
package httpstatus

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Check melaporkan kesiapan satu dependensi; nil berarti siap.
type Check func(ctx context.Context) error

// Handler membangun mux. checks diperiksa di /readyz dan /status; status
// mengembalikan objek apa pun yang bisa diserialisasi JSON.
func Handler(checks map[string]Check, status func() any, now func() time.Time) http.Handler {
	run := func(ctx context.Context) (map[string]string, bool) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		out := map[string]string{}
		ok := true
		for name, check := range checks {
			if err := check(ctx); err != nil {
				out[name] = err.Error()
				ok = false
				continue
			}
			out[name] = "ok"
		}
		return out, ok
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if _, ok := run(r.Context()); !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("belum siap\n"))
			return
		}
		_, _ = w.Write([]byte("siap\n"))
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		deps, ok := run(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Now          time.Time         `json:"now"`
			Ready        bool              `json:"ready"`
			Dependencies map[string]string `json:"dependencies"`
			Status       any               `json:"status"`
		}{now().UTC(), ok, deps, status()})
	})
	return mux
}
