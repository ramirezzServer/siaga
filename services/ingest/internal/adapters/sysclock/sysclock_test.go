package sysclock

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSleep(t *testing.T) {
	var c Clock
	if c.Now().IsZero() {
		t.Fatal("Now nol")
	}
	if err := c.Sleep(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := c.Sleep(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sleep dengan ctx batal: %v", err)
	}
}
