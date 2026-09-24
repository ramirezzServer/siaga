package nopub

import (
	"context"
	"testing"

	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

func TestPublisherCounts(t *testing.T) {
	var p Publisher
	for range 3 {
		if _, err := p.Publish(context.Background(), ports.Message{}); err != nil {
			t.Fatal(err)
		}
	}
	if p.Count() != 3 {
		t.Fatalf("Count = %d", p.Count())
	}
}
