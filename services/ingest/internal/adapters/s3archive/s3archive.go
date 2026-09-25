// Package s3archive menyimpan payload mentah di object storage berprotokol S3
// (Garage di lokal dan produksi). Kuncinya sama dengan fsarchive, ditambah
// awalan opsional (misal "raw/") supaya satu bucket bisa dipakai beberapa jenis
// arsip.
package s3archive

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // Content-MD5 adalah cek integritas transfer S3, bukan keamanan.
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// ErrInvalidKey menandai kunci yang tidak aman dipetakan ke objek.
var ErrInvalidKey = errors.New("kunci arsip tidak valid")

// DefaultMaxObjectBytes membatasi ukuran objek yang dibaca Get. Payload
// terbesar (CSV FIRMS 16 MiB sebelum dikompres) jauh di bawahnya.
const DefaultMaxObjectBytes = 64 << 20

// Config adalah koneksi ke satu bucket.
type Config struct {
	// Endpoint API S3, misal "http://127.0.0.1:3900" untuk Garage lokal.
	Endpoint string
	// Region sesuai s3_region Garage ("garage").
	Region string
	Bucket string
	// Prefix ditambahkan di depan setiap kunci; diakhiri "/" bila belum.
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
	// HTTPClient opsional; nil memakai klien bawaan SDK.
	HTTPClient *http.Client
	// MaxObjectBytes membatasi Get; nol = DefaultMaxObjectBytes.
	MaxObjectBytes int64
}

// Validate memeriksa konfigurasi tanpa jaringan.
func (c Config) Validate() error {
	var errs []error
	u, err := url.Parse(c.Endpoint)
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		errs = append(errs, fmt.Errorf("endpoint S3 %q harus URL http(s) lengkap", c.Endpoint))
	case u.User != nil:
		errs = append(errs, errors.New("endpoint S3 tidak boleh memuat kredensial; pakai access key terpisah"))
	}
	if c.Region == "" {
		errs = append(errs, errors.New("region S3 kosong"))
	}
	if !validBucket(c.Bucket) {
		errs = append(errs, fmt.Errorf("nama bucket %q tidak valid", c.Bucket))
	}
	if c.Prefix != "" && validKey(strings.TrimSuffix(c.Prefix, "/")) != nil {
		errs = append(errs, fmt.Errorf("awalan %q tidak valid", c.Prefix))
	}
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		errs = append(errs, errors.New("access key atau secret key S3 kosong"))
	}
	if c.MaxObjectBytes < 0 {
		errs = append(errs, errors.New("MaxObjectBytes negatif"))
	}
	return errors.Join(errs...)
}

// validBucket mengikuti aturan nama bucket S3 yang dipakai Garage.
func validBucket(b string) bool {
	if len(b) < 3 || len(b) > 63 || strings.Contains(b, "..") {
		return false
	}
	for i, r := range b {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || (r == '-' || r == '.') && i > 0 && i < len(b)-1
		if !ok {
			return false
		}
	}
	return true
}

// validKey menolak kunci yang di filesystem bisa keluar dari folder arsip,
// supaya arsip bisa disalin bolak-balik antara fsarchive dan S3.
func validKey(key string) error {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, `\`) {
		return fmt.Errorf("%w: %q", ErrInvalidKey, key)
	}
	for seg := range strings.SplitSeq(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("%w: %q", ErrInvalidKey, key)
		}
	}
	return nil
}

// Archive adalah ports.ArchiveStore di atas satu bucket.
type Archive struct {
	client   *s3.Client
	bucket   string
	prefix   string
	maxBytes int64
}

var _ ports.ArchiveStore = (*Archive)(nil)

// New membuat Archive. Tidak menghubungi server; pakai Check untuk itu.
func New(cfg Config) (*Archive, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	prefix := cfg.Prefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	maxBytes := cfg.MaxObjectBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxObjectBytes
	}
	opts := s3.Options{
		Region:       cfg.Region,
		BaseEndpoint: aws.String(cfg.Endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		// Garage melayani bucket di path, bukan subdomain.
		UsePathStyle: true,
		// Checksum CRC bawaan SDK baru memakai trailer aws-chunked; integritas
		// dijaga Content-MD5 di Put dan SHA-256 payload di kunci.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
		RetryMaxAttempts:           3,
	}
	if cfg.HTTPClient != nil {
		opts.HTTPClient = cfg.HTTPClient
	}
	return &Archive{client: s3.New(opts), bucket: cfg.Bucket, prefix: prefix, maxBytes: maxBytes}, nil
}

// Location mengembalikan lokasi arsip untuk log, tanpa kredensial.
func (a *Archive) Location() string { return "s3://" + a.bucket + "/" + a.prefix }

// Check memastikan bucket ada dan kredensial diterima.
func (a *Archive) Check(ctx context.Context) error {
	if _, err := a.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(a.bucket)}); err != nil {
		return fmt.Errorf("bucket %s tidak bisa dibuka (Garage jalan? `make up`; kredensial ARCHIVE_S3_* benar?): %w", a.bucket, err)
	}
	return nil
}

// Put menulis objek dengan Content-MD5 sehingga server menolak isi yang rusak
// di jalan. Menulis ulang kunci yang sama dengan isi yang sama tidak mengubah
// apa pun, jadi Put idempotent.
func (a *Archive) Put(ctx context.Context, key string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validKey(key); err != nil {
		return err
	}
	sum := md5.Sum(data) //nolint:gosec // lihat import.
	contentType := "application/octet-stream"
	if strings.HasSuffix(key, ".gz") {
		contentType = "application/gzip"
	}
	_, err := a.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(a.bucket),
		Key:           aws.String(a.prefix + key),
		Body:          bytes.NewReader(data),
		ContentLength: aws.Int64(int64(len(data))),
		ContentMD5:    aws.String(base64.StdEncoding.EncodeToString(sum[:])),
		ContentType:   aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("menulis %s: %w", key, err)
	}
	return nil
}

// Get membaca objek; ports.ErrArchiveNotFound bila tidak ada.
func (a *Archive) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validKey(key); err != nil {
		return nil, err
	}
	out, err := a.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(a.bucket), Key: aws.String(a.prefix + key)})
	if err != nil {
		if notFound(err) {
			return nil, fmt.Errorf("%w: %s", ports.ErrArchiveNotFound, key)
		}
		return nil, fmt.Errorf("membaca %s: %w", key, err)
	}
	defer func() { _ = out.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(out.Body, a.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("membaca isi %s: %w", key, err)
	}
	if int64(len(b)) > a.maxBytes {
		return nil, fmt.Errorf("objek %s lebih dari %d byte", key, a.maxBytes)
	}
	return b, nil
}

func notFound(err error) bool {
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var api smithy.APIError
	return errors.As(err, &api) && (api.ErrorCode() == "NotFound" || api.ErrorCode() == "NoSuchKey")
}

// List memakai ListObjectsV2, yang mengurutkan kunci per byte UTF-8.
func (a *Archive) List(ctx context.Context, prefix, startAfter string) iter.Seq2[ports.ArchiveObject, error] {
	return func(yield func(ports.ArchiveObject, error) bool) {
		in := &s3.ListObjectsV2Input{Bucket: aws.String(a.bucket), Prefix: aws.String(a.prefix + prefix)}
		if startAfter != "" {
			in.StartAfter = aws.String(a.prefix + startAfter)
		}
		pages := s3.NewListObjectsV2Paginator(a.client, in)
		for pages.HasMorePages() {
			if err := ctx.Err(); err != nil {
				yield(ports.ArchiveObject{}, err)
				return
			}
			page, err := pages.NextPage(ctx)
			if err != nil {
				yield(ports.ArchiveObject{}, fmt.Errorf("mendaftar arsip %q: %w", prefix, err))
				return
			}
			for _, o := range page.Contents {
				key, ok := strings.CutPrefix(aws.ToString(o.Key), a.prefix)
				if !ok {
					continue
				}
				if !yield(ports.ArchiveObject{Key: key, Size: aws.ToInt64(o.Size)}, nil) {
					return
				}
			}
		}
	}
}
