package quake

import (
	"strings"
	"unicode"
)

// MaxMMI adalah skala MMI tertinggi (XII).
const MaxMMI = 12

// FeltMention adalah satu penyebutan wilayah di kolom "Dirasakan" BMKG,
// misal "II - III Kab. Manggarai": skala MMI (rentang atau satu nilai) dan
// nama wilayah apa adanya.
type FeltMention struct {
	MMIMin, MMIMax int
	Name           string
}

// MMI adalah skala yang dipakai untuk satu penyebutan: batas bawah rentang,
// karena BMKG menulis rentang saat laporan di wilayah itu berbeda-beda dan
// batas bawah adalah yang pasti dialami.
func (m FeltMention) MMI() int { return m.MMIMin }

// ParseFelt mengurai kolom "Dirasakan" BMKG, misal
// "III-IV Manggarai, II - III Kota Bima, II Kab. Lombok Barat". Bagian
// dipisahkan koma; setiap bagian diawali angka Romawi I–XII atau rentang
// "A-B" (spasi di sekitar tanda hubung boleh), lalu nama wilayah. Bagian
// yang tidak berbentuk begitu dikembalikan di unparsed, tanpa dibuang diam-diam.
// Fungsi murni; urutan penyebutan dipertahankan.
func ParseFelt(s string) (mentions []FeltMention, unparsed []string) {
	for part := range strings.SplitSeq(s, ",") {
		part = strings.Join(strings.Fields(part), " ")
		if part == "" {
			continue
		}
		m, ok := parseFeltPart(part)
		if !ok {
			unparsed = append(unparsed, part)
			continue
		}
		mentions = append(mentions, m)
	}
	return mentions, unparsed
}

func parseFeltPart(part string) (FeltMention, bool) {
	lo, rest, ok := leadingRoman(part)
	if !ok {
		return FeltMention{}, false
	}
	hi := lo
	if r, found := strings.CutPrefix(strings.TrimLeft(rest, " "), "-"); found {
		v, after, ok := leadingRoman(strings.TrimLeft(r, " "))
		if !ok || v < lo {
			return FeltMention{}, false
		}
		hi, rest = v, after
	}
	if rest == "" || rest[0] != ' ' {
		return FeltMention{}, false
	}
	name := strings.TrimSpace(rest)
	if name == "" || !strings.ContainsFunc(name, unicode.IsLetter) {
		return FeltMention{}, false
	}
	return FeltMention{MMIMin: lo, MMIMax: hi, Name: name}, true
}

// leadingRoman membaca angka Romawi huruf besar I–XII di awal s. Angka harus
// diikuti spasi, tanda hubung, atau akhir teks, supaya nama yang diawali
// huruf I, V, atau X (misal "Indramayu") tidak terbaca sebagai skala.
func leadingRoman(s string) (int, string, bool) {
	n := 0
	for n < len(s) && strings.IndexByte("IVX", s[n]) >= 0 {
		n++
	}
	if n == 0 || (n < len(s) && s[n] != ' ' && s[n] != '-') {
		return 0, s, false
	}
	v, ok := romanValue(s[:n])
	if !ok {
		return 0, s, false
	}
	return v, s[n:], true
}

var romanNumerals = [...]string{"I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X", "XI", "XII"}

func romanValue(s string) (int, bool) {
	for i, r := range romanNumerals {
		if r == s {
			return i + 1, true
		}
	}
	return 0, false
}
