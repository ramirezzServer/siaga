package quake

import "github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"

// newDigester memulai hash isi (lihat hazard.Digester).
func newDigester(domain string) *hazard.Digester { return hazard.NewDigester(domain) }
