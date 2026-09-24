// Package schedule menghitung jeda antar-polling: interval normal, backoff
// eksponensial saat gagal, permintaan Retry-After dari sumber, dan jitter
// supaya konektor tidak serempak menembak sumber yang sama.
package schedule

import "time"

// MaxRetryAfter membatasi Retry-After dari sumber supaya nilai ekstrem (atau
// salah format) tidak menghentikan konektor terlalu lama.
const MaxRetryAfter = time.Hour

// Next mengembalikan jeda sebelum polling berikutnya.
//   - failures = jumlah kegagalan berturut-turut (0 bila polling terakhir sukses);
//   - jeda gagal = interval * 2^failures, dibatasi maxBackoff tetapi tidak
//     pernah kurang dari interval;
//   - retryAfter dari sumber dihormati bila lebih lama, dibatasi MaxRetryAfter.
func Next(interval time.Duration, failures int, maxBackoff, retryAfter time.Duration) time.Duration {
	d := interval
	for i := 0; i < failures && d < maxBackoff; i++ {
		d *= 2
	}
	d = min(d, maxBackoff)
	d = max(d, interval)
	return max(d, min(retryAfter, MaxRetryAfter))
}

// Jitter menggeser d secara acak dalam rentang ±frac*d. u adalah bilangan acak
// seragam di [0, 1) yang disuntikkan pemanggil supaya fungsi ini tetap murni.
// frac dibatasi ke [0, 0.5].
func Jitter(d time.Duration, frac, u float64) time.Duration {
	frac = min(max(frac, 0), 0.5)
	u = min(max(u, 0), 1)
	return d + time.Duration((2*u-1)*frac*float64(d))
}
