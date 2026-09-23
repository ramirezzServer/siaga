// Package ports mendefinisikan batas antara logika aplikasi dan dunia luar.
// Adapter (dump SQL, PostgreSQL) mengimplementasikan interface di sini.
package ports

import (
	"context"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/region"
)

// RawRegion adalah baris wilayah apa adanya dari sumber, sebelum divalidasi domain.
type RawRegion struct {
	Code   string
	Name   string
	Path   []byte // geometri dalam format asli sumber
	Origin string // file asal, untuk pesan kesalahan
}

// RegionSource menyediakan baris wilayah mentah.
type RegionSource interface {
	Name() string
	Version() string
	Each(ctx context.Context, fn func(RawRegion) error) error
}

// UpsertResult merangkum hasil penulisan ke penyimpanan.
type UpsertResult struct {
	Inserted  int
	Updated   int
	Unchanged int
}

// RegionStore menyimpan wilayah. UpsertAll harus atomik: semua baris masuk, atau tidak sama sekali.
type RegionStore interface {
	UpsertAll(ctx context.Context, regions []region.Region, source, version string) (UpsertResult, error)
	// CodesUnder mengembalikan semua kode tersimpan di bawah (dan termasuk) provinsi.
	CodesUnder(ctx context.Context, province string) ([]string, error)
}
