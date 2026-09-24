package area

import "testing"

func TestParseBox(t *testing.T) {
	b, err := ParseBox(" 106.3, -7.9,108.9 ,-5.7")
	if err != nil || b != JawaBarat || b.String() != "106.3,-7.9,108.9,-5.7" {
		t.Fatalf("%v %v", b, err)
	}
	for _, bad := range []string{"", "1,2,3", "a,b,c,d", "108,-7,107,-6", "106,-6,107,-7", "90,-7,107,-6", "106,-7,150,-6", "106,-13,107,-6", "106,-7,107,8"} {
		if _, err := ParseBox(bad); err == nil {
			t.Errorf("ParseBox(%q) seharusnya gagal", bad)
		}
	}
}
