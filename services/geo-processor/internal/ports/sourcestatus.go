package ports

import (
	"context"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/sourcestatus"
)

// SourceStatusStore menyimpan keadaan konektor ingest di ts.source_status
// (ADR 0030).
type SourceStatusStore interface {
	// Update membaca keadaan konektor dengan kunci baris (prev nil bila
	// belum ada), memanggil fn, lalu menyimpan hasilnya bila fn
	// mengembalikan true, semuanya dalam satu transaksi. Mengembalikan
	// nilai kedua fn.
	Update(ctx context.Context, connector string, fn func(prev *sourcestatus.State) (sourcestatus.State, bool)) (bool, error)
}
