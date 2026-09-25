// Package archiveurl membuka arsip payload mentah dari satu URL, supaya
// ingest, replay, dan alat arsip memakai konfigurasi yang sama:
//
//	/path/ke/folder                 folder lokal (fsarchive)
//	file:///path/ke/folder          sama, bentuk URL
//	s3://bucket/awalan?endpoint=http://127.0.0.1:3900[&region=garage]
//
// Kredensial S3 tidak pernah ditulis di URL (URL ikut tercetak di log);
// kredensial diberikan terpisah lewat Credentials.
package archiveurl

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/fsarchive"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/s3archive"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// DefaultRegion adalah s3_region bawaan Garage.
const DefaultRegion = "garage"

// Credentials adalah access key S3 (ARCHIVE_S3_ACCESS_KEY_ID dan
// ARCHIVE_S3_SECRET_ACCESS_KEY).
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
}

// Store adalah arsip yang bisa ditulis dan dibaca beserta lokasinya untuk log.
type Store interface {
	ports.ArchiveStore
	// Location tanpa kredensial, aman dicetak.
	Location() string
	// Check memastikan arsip bisa dipakai (folder bisa ditulis, bucket ada).
	Check(ctx context.Context) error
}

// Open membuka arsip dari raw. client dipakai untuk S3 (nil = bawaan SDK).
func Open(raw string, creds Credentials, client *http.Client) (Store, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("URL arsip kosong")
	}
	if !strings.Contains(raw, "://") {
		return openDir(raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		// Galat url.Parse memuat URL utuh; URL arsip tidak berisi rahasia.
		return nil, fmt.Errorf("URL arsip: %w", err)
	}
	if u.User != nil {
		return nil, errors.New("URL arsip tidak boleh memuat kredensial; pakai ARCHIVE_S3_ACCESS_KEY_ID dan ARCHIVE_S3_SECRET_ACCESS_KEY")
	}
	switch u.Scheme {
	case "file":
		if u.Host != "" && u.Host != "localhost" {
			return nil, fmt.Errorf("URL file %q harus berbentuk file:///path", raw)
		}
		return openDir(u.Path)
	case "s3":
		return openS3(u, creds, client)
	default:
		return nil, fmt.Errorf("skema URL arsip %q tidak dikenal (file atau s3)", u.Scheme)
	}
}

func openDir(dir string) (Store, error) {
	if dir == "" {
		return nil, errors.New("folder arsip kosong")
	}
	a, err := fsarchive.New(dir)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func openS3(u *url.URL, creds Credentials, client *http.Client) (Store, error) {
	q := u.Query()
	for k := range q {
		if !slices.Contains([]string{"endpoint", "region"}, k) {
			return nil, fmt.Errorf("parameter URL arsip %q tidak dikenal (endpoint, region)", k)
		}
	}
	region := q.Get("region")
	if region == "" {
		region = DefaultRegion
	}
	if q.Get("endpoint") == "" {
		return nil, errors.New("URL arsip s3 butuh ?endpoint=, misal s3://siaga-arsip/raw?endpoint=http://127.0.0.1:3900")
	}
	a, err := s3archive.New(s3archive.Config{
		Endpoint: q.Get("endpoint"), Region: region, Bucket: u.Host,
		Prefix:      strings.Trim(u.Path, "/"),
		AccessKeyID: creds.AccessKeyID, SecretAccessKey: creds.SecretAccessKey,
		HTTPClient: client,
	})
	if err != nil {
		return nil, fmt.Errorf("URL arsip s3: %w", err)
	}
	return a, nil
}
