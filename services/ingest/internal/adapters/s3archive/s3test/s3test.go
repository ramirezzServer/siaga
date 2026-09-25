// Package s3test adalah server S3 tiruan dalam proses untuk test: satu bucket,
// PUT/GET/HEAD objek, HEAD bucket, dan ListObjectsV2 dengan paginasi. Cukup
// untuk menguji adapter s3archive tanpa Docker; perilaku Garage asli diuji di
// uji integrasi.
package s3test

import (
	"crypto/md5" //nolint:gosec // memeriksa Content-MD5 dari klien.
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Server adalah S3 tiruan.
type Server struct {
	*httptest.Server
	Bucket string
	// PageSize adalah max-keys bawaan ListObjectsV2 (S3 asli 1.000); kecil
	// supaya paginasi ikut teruji.
	PageSize int

	mu      sync.Mutex
	objects map[string][]byte
	// failNext membuat request berikutnya yang cocok dijawab 500.
	failNext map[string]int
	requests []string
}

// New menjalankan server dengan satu bucket; ditutup otomatis di akhir test.
func New(t testing.TB, bucket string) *Server {
	t.Helper()
	s := &Server{Bucket: bucket, PageSize: 100, objects: map[string][]byte{}, failNext: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

// Objects mengembalikan salinan isi bucket.
func (s *Server) Objects() map[string][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]byte, len(s.objects))
	for k, v := range s.objects {
		out[k] = slices.Clone(v)
	}
	return out
}

// Set menaruh objek langsung, misal isi yang rusak untuk uji verifikasi.
func (s *Server) Set(key string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = slices.Clone(data)
}

// FailNext membuat n request berikutnya dengan metode method dijawab 500.
func (s *Server) FailNext(method string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext[method] = n
}

// Requests mengembalikan "METODE path" semua request yang diterima.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r.Method+" "+r.URL.Path)
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
		writeError(w, http.StatusForbidden, "AccessDenied", "tanpa tanda tangan SigV4")
		return
	}
	if n := s.failNext[r.Method]; n > 0 {
		s.failNext[r.Method] = n - 1
		writeError(w, http.StatusInternalServerError, "InternalError", "galat tiruan")
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/"+s.Bucket)
	if !ok || (rest != "" && !strings.HasPrefix(rest, "/")) {
		writeError(w, http.StatusNotFound, "NoSuchBucket", "bucket tidak ada")
		return
	}
	key := strings.TrimPrefix(rest, "/")
	switch {
	case key == "" && r.Method == http.MethodHead:
		w.WriteHeader(http.StatusOK)
	case key == "" && r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
		s.list(w, r)
	case key != "" && r.Method == http.MethodPut:
		s.put(w, r, key)
	case key != "" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		b, ok := s.objects[key]
		if !ok {
			writeError(w, http.StatusNotFound, "NoSuchKey", "kunci tidak ada")
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(b)
		}
	default:
		writeError(w, http.StatusNotImplemented, "NotImplemented", r.Method+" "+r.URL.String())
	}
}

func (s *Server) put(w http.ResponseWriter, r *http.Request, key string) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "IncompleteBody", err.Error())
		return
	}
	if enc := r.Header.Get("Content-Encoding"); strings.Contains(enc, "aws-chunked") {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "aws-chunked tidak didukung tiruan")
		return
	}
	if want := r.Header.Get("Content-MD5"); want != "" {
		sum := md5.Sum(b) //nolint:gosec // lihat import.
		if base64.StdEncoding.EncodeToString(sum[:]) != want {
			writeError(w, http.StatusBadRequest, "BadDigest", "Content-MD5 tidak cocok")
			return
		}
	}
	s.objects[key] = b
	w.Header().Set("ETag", fmt.Sprintf("%q", fmt.Sprintf("%x", md5.Sum(b)))) //nolint:gosec // lihat import.
	w.WriteHeader(http.StatusOK)
}

type listResult struct {
	XMLName               xml.Name  `xml:"ListBucketResult"`
	Name                  string    `xml:"Name"`
	Prefix                string    `xml:"Prefix"`
	KeyCount              int       `xml:"KeyCount"`
	MaxKeys               int       `xml:"MaxKeys"`
	IsTruncated           bool      `xml:"IsTruncated"`
	ContinuationToken     string    `xml:"ContinuationToken,omitempty"`
	NextContinuationToken string    `xml:"NextContinuationToken,omitempty"`
	StartAfter            string    `xml:"StartAfter,omitempty"`
	Contents              []content `xml:"Contents"`
}

type content struct {
	Key  string `xml:"Key"`
	Size int    `xml:"Size"`
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	prefix, after := q.Get("prefix"), q.Get("start-after")
	if tok := q.Get("continuation-token"); tok != "" {
		after = tok // token = kunci terakhir halaman sebelumnya
	}
	limit := s.PageSize
	if mk, err := strconv.Atoi(q.Get("max-keys")); err == nil && mk > 0 && mk < limit {
		limit = mk
	}
	keys := make([]string, 0, len(s.objects))
	for k := range s.objects {
		if strings.HasPrefix(k, prefix) && k > after {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	res := listResult{Name: s.Bucket, Prefix: prefix, MaxKeys: limit, ContinuationToken: q.Get("continuation-token"), StartAfter: q.Get("start-after")}
	if len(keys) > limit {
		keys = keys[:limit]
		res.IsTruncated = true
		res.NextContinuationToken = keys[len(keys)-1]
	}
	for _, k := range keys {
		res.Contents = append(res.Contents, content{Key: k, Size: len(s.objects[k])})
	}
	res.KeyCount = len(res.Contents)
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(res)
}

type errorBody struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_ = xml.NewEncoder(w).Encode(errorBody{Code: code, Message: msg})
}
