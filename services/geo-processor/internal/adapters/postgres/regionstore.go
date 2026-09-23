// Package postgres mengimplementasikan port penyimpanan dengan PostgreSQL (pgx).
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres/regionsql"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/region"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// RegionStore menyimpan wilayah ke ref.region.
type RegionStore struct {
	conn *pgx.Conn
}

var _ ports.RegionStore = (*RegionStore)(nil)

// NewRegionStore membungkus koneksi yang sudah terbuka. Pemanggil yang menutup koneksi.
func NewRegionStore(conn *pgx.Conn) *RegionStore { return &RegionStore{conn: conn} }

// UpsertAll menulis semua wilayah dalam satu transaksi: COPY ke tabel staging,
// lalu satu INSERT … ON CONFLICT. Bila satu baris melanggar constraint, tidak ada
// yang tersimpan.
func (s *RegionStore) UpsertAll(ctx context.Context, regions []region.Region, source, version string) (res ports.UpsertResult, err error) {
	tx, err := s.conn.Begin(ctx)
	if err != nil {
		return res, fmt.Errorf("memulai transaksi: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, rollback(ctx, tx))
		}
	}()

	if _, err = tx.Exec(ctx, regionsql.CreateStaging); err != nil {
		return res, fmt.Errorf("membuat staging: %w", err)
	}

	rows := make([][]any, 0, len(regions))
	for _, r := range regions {
		geo, mErr := r.Boundary.MarshalGeoJSON()
		if mErr != nil {
			return res, fmt.Errorf("serialisasi %s: %w", r.Code, mErr)
		}
		rows = append(rows, []any{r.Code.String(), string(r.Kind()), r.Name, string(geo)})
	}
	if _, err = tx.CopyFrom(ctx, pgx.Identifier{regionsql.StagingTable}, regionsql.StagingColumns, pgx.CopyFromRows(rows)); err != nil {
		return res, fmt.Errorf("COPY staging: %w", err)
	}

	written, err := tx.Query(ctx, regionsql.Upsert, source, version)
	if err != nil {
		return res, fmt.Errorf("upsert ref.region: %w", err)
	}
	inserted, err := pgx.CollectRows(written, pgx.RowTo[bool])
	if err != nil {
		return res, fmt.Errorf("upsert ref.region: %w", err)
	}
	for _, isNew := range inserted {
		if isNew {
			res.Inserted++
		} else {
			res.Updated++
		}
	}
	res.Unchanged = len(regions) - len(inserted)

	if err = tx.Commit(ctx); err != nil {
		return res, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}

// CodesUnder mengembalikan kode provinsi dan semua turunannya yang tersimpan.
func (s *RegionStore) CodesUnder(ctx context.Context, province string) ([]string, error) {
	rows, err := s.conn.Query(ctx, regionsql.CodesUnder, province)
	if err != nil {
		return nil, fmt.Errorf("membaca kode: %w", err)
	}
	codes, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("membaca kode: %w", err)
	}
	return codes, nil
}

func rollback(ctx context.Context, tx pgx.Tx) error {
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return fmt.Errorf("rollback: %w", err)
	}
	return nil
}
