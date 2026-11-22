package region

import "math"

// earthRadiusKm adalah jari-jari rata-rata bumi (IUGG), sama dengan paket quake.
const earthRadiusKm = 6371.0088

// HaversineKm adalah jarak lingkaran besar antara dua titik (derajat).
func HaversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp := p2 - p1
	dl := (lon2 - lon1) * math.Pi / 180
	h := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * earthRadiusKm * math.Asin(math.Sqrt(math.Min(1, h)))
}

// Contains melaporkan apakah titik (lintang, bujur) ada di dalam batas:
// di dalam cincin luar salah satu poligon dan tidak di dalam lubangnya.
// Titik tepat di garis batas bisa terhitung di dalam atau di luar.
func (m MultiPolygon) Contains(lat, lon float64) bool {
	for _, poly := range m {
		if len(poly) == 0 || !ringContains(poly[0], lat, lon) {
			continue
		}
		inHole := false
		for _, hole := range poly[1:] {
			if ringContains(hole, lat, lon) {
				inHole = true
				break
			}
		}
		if !inHole {
			return true
		}
	}
	return false
}

// ringContains adalah uji ray casting di bidang bujur–lintang.
func ringContains(r Ring, lat, lon float64) bool {
	in := false
	for i, j := 0, len(r)-1; i < len(r); j, i = i, i+1 {
		xi, yi := r[i][0], r[i][1]
		xj, yj := r[j][0], r[j][1]
		if (yi > lat) != (yj > lat) && lon < (xj-xi)*(lat-yi)/(yj-yi)+xi {
			in = !in
		}
	}
	return in
}

// DistanceKm adalah jarak terdekat dari titik (lintang, bujur) ke batas
// wilayah: 0 bila titik di dalamnya, selain itu jarak haversine ke titik
// terdekat di tepi poligon. Titik terdekat di tiap sisi dicari di proyeksi
// ekuirektangular yang berpusat di titik itu; jaraknya, dan jarak ke ujung
// sisi, dihitung dengan haversine, jadi hasilnya tidak pernah lebih besar
// dari jarak ke titik sudut mana pun dan galatnya kecil untuk ratusan km.
func (m MultiPolygon) DistanceKm(lat, lon float64) float64 {
	if m.Contains(lat, lon) {
		return 0
	}
	kx := math.Max(math.Cos(lat*math.Pi/180), 1e-6)
	best := math.Inf(1)
	for _, poly := range m {
		for _, ring := range poly {
			for i := 1; i < len(ring); i++ {
				a, b := ring[i-1], ring[i]
				ax, ay := (a[0]-lon)*kx, a[1]-lat
				dx, dy := (b[0]-a[0])*kx, b[1]-a[1]
				t := 0.0
				if l2 := dx*dx + dy*dy; l2 > 0 {
					t = math.Max(0, math.Min(1, -(ax*dx+ay*dy)/l2))
				}
				best = math.Min(best, HaversineKm(lat, lon, a[1]+t*(b[1]-a[1]), a[0]+t*(b[0]-a[0])))
				best = math.Min(best, HaversineKm(lat, lon, a[1], a[0]))
				best = math.Min(best, HaversineKm(lat, lon, b[1], b[0]))
			}
		}
	}
	return best
}
