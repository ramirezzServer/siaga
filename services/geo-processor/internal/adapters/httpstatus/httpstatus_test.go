package httpstatus

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEndpoints(t *testing.T) {
	var dbErr error
	h := Handler(map[string]Check{
		"postgres": func(context.Context) error { return dbErr },
	}, func() any { return map[string]int{"received": 3} }, func() time.Time { return time.Unix(0, 0) })

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
		return rec
	}
	if rec := get("/healthz"); rec.Code != http.StatusOK {
		t.Errorf("/healthz %d", rec.Code)
	}
	if rec := get("/readyz"); rec.Code != http.StatusOK {
		t.Errorf("/readyz %d", rec.Code)
	}
	dbErr = errors.New("mati")
	if rec := get("/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz saat database mati %d", rec.Code)
	}
	rec := get("/status")
	var body struct {
		Ready        bool
		Dependencies map[string]string
		Status       map[string]int
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Ready || body.Dependencies["postgres"] != "mati" || body.Status["received"] != 3 {
		t.Errorf("/status %+v", body)
	}
}
