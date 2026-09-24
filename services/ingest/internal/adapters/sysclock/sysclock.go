// Package sysclock adalah ports.Clock di atas jam sistem.
package sysclock

import (
	"context"
	"time"
)

// Clock memakai time.Now dan timer sungguhan.
type Clock struct{}

// Now mengembalikan waktu sekarang.
func (Clock) Now() time.Time { return time.Now() }

// Sleep menunggu d atau sampai ctx selesai.
func (Clock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
