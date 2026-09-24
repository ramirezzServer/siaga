// Package nopub adalah Publisher yang tidak menerbitkan apa pun, untuk mode
// rekam (-publish=false): payload tetap diambil, diarsipkan, dan divalidasi.
package nopub

import (
	"context"
	"sync/atomic"

	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Publisher membuang pesan dan menghitungnya.
type Publisher struct{ n atomic.Int64 }

// Publish membuang msg.
func (p *Publisher) Publish(context.Context, ports.Message) (ports.PublishResult, error) {
	p.n.Add(1)
	return ports.PublishResult{}, nil
}

// Count mengembalikan jumlah pesan yang dibuang.
func (p *Publisher) Count() int64 { return p.n.Load() }
