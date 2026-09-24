package httpstatus

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/runner"
)

func TestEndpoints(t *testing.T) {
	ready := false
	now := time.Date(2026, 9, 24, 3, 0, 0, 0, time.UTC)
	h := Handler(func() bool { return ready },
		func() []runner.Status { return []runner.Status{{Connector: "bmkg-autogempa", ConsecutiveFailures: 2}} },
		func() time.Time { return now })

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		return rec
	}
	if rec := get("/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("/healthz %d", rec.Code)
	}
	if rec := get("/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz saat belum siap %d", rec.Code)
	}
	ready = true
	if rec := get("/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("/readyz saat siap %d", rec.Code)
	}
	rec := get("/status")
	var body struct {
		Now        time.Time       `json:"now"`
		Ready      bool            `json:"ready"`
		Connectors []runner.Status `json:"connectors"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Ready || !body.Now.Equal(now) || len(body.Connectors) != 1 || body.Connectors[0].ConsecutiveFailures != 2 {
		t.Fatalf("/status %+v", body)
	}
	if rec := httptest.NewRecorder(); true {
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/status", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST /status %d", rec.Code)
		}
	}
}
