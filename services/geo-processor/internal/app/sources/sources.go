// Package sources adalah use case status sumber data geo-processor: setiap
// hasil polling konektor ingest (stream STATUS) dilipat ke keadaan
// terakhirnya di ts.source_status (ADR 0030).
package sources

import (
	"context"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/sourcestatus"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// Service memvalidasi lalu menyimpan hasil polling.
type Service struct {
	store ports.SourceStatusStore
	now   func() time.Time
}

// New membuat Service. now dipakai untuk menolak waktu di masa depan.
func New(store ports.SourceStatusStore, now func() time.Time) *Service {
	return &Service{store: store, now: now}
}

// Result merangkum dampak satu hasil polling.
type Result struct {
	// Changed false berarti pesan ulangan atau pesan terlambat yang tidak
	// mengubah apa pun.
	Changed bool
}

// Record menyimpan satu hasil polling. Galat yang membungkus
// sourcestatus.ErrInvalid tidak akan berhasil bila diulang.
func (s *Service) Record(ctx context.Context, p sourcestatus.Poll) (Result, error) {
	if err := p.Validate(s.now()); err != nil {
		return Result{}, err
	}
	var invalid error
	changed, err := s.store.Update(ctx, p.Connector, func(prev *sourcestatus.State) (sourcestatus.State, bool) {
		next, applied := sourcestatus.Apply(prev, p)
		if applied {
			// Keadaan lama yang rusak (misal diubah manual) tidak ditimpa diam-diam.
			if invalid = next.Validate(); invalid != nil {
				return next, false
			}
		}
		return next, applied
	})
	if err == nil {
		err = invalid
	}
	return Result{Changed: changed && invalid == nil}, err
}
