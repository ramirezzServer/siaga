// Package httpfetch mengambil data sumber lewat HTTP dengan batas waktu,
// batas ukuran, conditional request, dan penanganan Retry-After.
package httpfetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// UserAgent memperkenalkan SIAGA ke pemilik sumber, sesuai etika pemakaian API publik.
const UserAgent = "SIAGA-ingest/0.1 (+https://github.com/ramirezzServer/siaga)"

// DefaultMaxBytes dipakai bila Request.MaxBytes kosong.
const DefaultMaxBytes = 8 << 20

// ErrTooLarge dikembalikan bila respons melebihi MaxBytes.
var ErrTooLarge = errors.New("respons melebihi batas ukuran")

// StatusError adalah respons HTTP yang bukan 200 atau 304.
type StatusError struct {
	Status int
	Body   string // potongan awal isi respons untuk diagnosis
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body)
}

// HTTPStatus mengembalikan kode status HTTP (ports.StatusCoder).
func (e *StatusError) HTTPStatus() int { return e.Status }

var _ ports.StatusCoder = (*StatusError)(nil)

// Fetcher adalah ports.Fetcher di atas net/http.
type Fetcher struct {
	client *http.Client
	now    func() time.Time
}

// New membuat Fetcher dengan batas waktu per request.
func New(timeout time.Duration) *Fetcher {
	return &Fetcher{
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConnsPerHost:   4,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: timeout,
			},
		},
		now: time.Now,
	}
}

// Fetch menjalankan GET.
func (f *Fetcher) Fetch(ctx context.Context, r ports.Request) (ports.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL, nil)
	if err != nil {
		return ports.Response{}, fmt.Errorf("membuat request: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)
	if r.Accept != "" {
		req.Header.Set("Accept", r.Accept)
	}
	if r.ETag != "" {
		req.Header.Set("If-None-Match", r.ETag)
	}
	if r.LastModified != "" {
		req.Header.Set("If-Modified-Since", r.LastModified)
	}
	for k, v := range r.Header {
		req.Header.Set(k, v)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return ports.Response{}, redact(err, r.Secrets)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotModified:
		return ports.Response{NotModified: true}, nil
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode == http.StatusServiceUnavailable:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return ports.Response{}, &ports.RetryAfterError{
			Status: resp.StatusCode,
			After:  parseRetryAfter(resp.Header.Get("Retry-After"), f.now()),
		}
	case resp.StatusCode != http.StatusOK:
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return ports.Response{}, &StatusError{Status: resp.StatusCode, Body: ports.Redact(strings.TrimSpace(string(snippet)), r.Secrets)}
	}

	limit := r.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return ports.Response{}, fmt.Errorf("membaca isi respons: %w", err)
	}
	if int64(len(body)) > limit {
		return ports.Response{}, fmt.Errorf("%w (%d byte)", ErrTooLarge, limit)
	}
	return ports.Response{
		Body:         body,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}, nil
}

// redact menyamarkan rahasia di galat net/http, yang menyertakan URL lengkap
// (misal `Get "https://…/KEY/…": dial tcp …`). Galat lain dikembalikan apa
// adanya karena tidak memuat URL.
func redact(err error, secrets []string) error {
	var ue *url.Error
	if len(secrets) == 0 || !errors.As(err, &ue) {
		return err
	}
	return &url.Error{Op: ue.Op, URL: ports.Redact(ue.URL, secrets), Err: redactedError{ue.Err, secrets}}
}

// redactedError menyamarkan rahasia di pesan penyebab, tetapi tetap bisa
// dibuka dengan errors.Is/As (misal context.DeadlineExceeded).
type redactedError struct {
	err     error
	secrets []string
}

func (c redactedError) Error() string { return ports.Redact(c.err.Error(), c.secrets) }
func (c redactedError) Unwrap() error { return c.err }

// parseRetryAfter membaca Retry-After dalam detik atau tanggal HTTP.
// Nilai yang tidak bisa dibaca atau negatif dianggap nol.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		if secs <= 0 || secs > int64(24*time.Hour/time.Second) {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}
