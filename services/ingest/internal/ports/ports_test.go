package ports

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBrief(t *testing.T) {
	short := "https://api.bmkg.go.id/publik/prakiraan-cuaca?adm4=32.73.01.1001"
	if got := Brief(short, nil); got != short {
		t.Errorf("URL pendek diubah: %s", got)
	}
	if got := Brief("https://x/a", nil); got != "https://x/a" {
		t.Errorf("URL tanpa query diubah: %s", got)
	}
	query := "latitude=" + strings.Repeat("-6.25,", 50) + "&apikey=RAHASIA"
	got := Brief("https://x/v1?"+query, []string{"RAHASIA"})
	wantPrefix := "https://x/v1?" + query[:MaxBriefQuery] + "…(+"
	if !strings.HasPrefix(got, wantPrefix) || !strings.HasSuffix(got, " karakter)") {
		t.Errorf("Brief = %s", got)
	}
	// Rahasia di bagian yang dipotong pun tidak pernah muncul sebagian.
	secret := strings.Repeat("K", 100)
	if got := Brief("https://x/v1?key="+secret, []string{secret}); strings.Contains(got, "K") {
		t.Errorf("potongan rahasia tertulis: %s", got)
	}
	// Potongan tidak memotong di tengah karakter multibyte.
	if got := Brief("https://x/?"+strings.Repeat("é", 100), nil); !utf8.ValidString(got) {
		t.Errorf("UTF-8 rusak: %q", got)
	}
	r := Request{URL: "https://x/?" + strings.Repeat("a", 200), Secrets: nil}
	if r.Brief() != Brief(r.URL, nil) {
		t.Error("Request.Brief berbeda dengan Brief")
	}
}
