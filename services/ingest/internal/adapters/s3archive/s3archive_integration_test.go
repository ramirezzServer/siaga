//go:build integration

package s3archive

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/archivetest"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// TestGarage menjalankan uji kesesuaian terhadap Garage asli (`make up`).
// Setiap subtest memakai awalan sendiri di bawah "uji-integrasi/" dan
// menghapus objeknya setelah selesai.
func TestGarage(t *testing.T) {
	endpoint := os.Getenv("SIAGA_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("SIAGA_TEST_S3_ENDPOINT kosong; jalankan lewat `make test-integration`")
	}
	base := Config{
		Endpoint: endpoint, Region: "garage", Bucket: os.Getenv("ARCHIVE_BUCKET"),
		AccessKeyID: os.Getenv("ARCHIVE_S3_ACCESS_KEY_ID"), SecretAccessKey: os.Getenv("ARCHIVE_S3_SECRET_ACCESS_KEY"),
	}
	run := time.Now().UTC().Format("20060102T150405.000000000")
	n := 0
	archivetest.Run(t, func(t *testing.T) ports.ArchiveStore {
		n++
		cfg := base
		cfg.Prefix = fmt.Sprintf("uji-integrasi/%s-%d", run, n)
		a, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Check(t.Context()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { purge(t, a) })
		return a
	}, archivetest.Options{Many: 1100}) // > 1.000: paginasi ListObjectsV2 asli
}

func purge(t *testing.T, a *Archive) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for o, err := range a.List(ctx, "", "") {
		if err != nil {
			t.Errorf("membersihkan: %v", err)
			return
		}
		if _, err := a.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(a.bucket), Key: aws.String(a.prefix + o.Key)}); err != nil {
			t.Errorf("menghapus %s: %v", o.Key, err)
			return
		}
	}
}
