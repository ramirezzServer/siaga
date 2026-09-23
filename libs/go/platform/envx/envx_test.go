package envx_test

import (
	"strings"
	"testing"

	"github.com/ramirezzServer/siaga/libs/go/platform/envx"
)

func mapLookup(m map[string]string) envx.Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestReaderCollectsAllErrors(t *testing.T) {
	t.Parallel()
	r := envx.NewReader(mapLookup(map[string]string{"B": "  "}))
	_ = r.Required("A")
	_ = r.Required("B")
	err := r.Err()
	if err == nil || !strings.Contains(err.Error(), "A") || !strings.Contains(err.Error(), "B") {
		t.Fatalf("Err() = %v; harus menyebut A dan B", err)
	}
}

func TestDefault(t *testing.T) {
	t.Parallel()
	r := envx.NewReader(mapLookup(map[string]string{"SET": "x", "BLANK": ""}))
	if got := r.Default("SET", "y"); got != "x" {
		t.Errorf("Default(SET) = %q", got)
	}
	if got := r.Default("BLANK", "y"); got != "y" {
		t.Errorf("Default(BLANK) = %q", got)
	}
	if got := r.Default("MISSING", "y"); got != "y" {
		t.Errorf("Default(MISSING) = %q", got)
	}
	if r.Err() != nil {
		t.Errorf("Default tidak boleh mencatat kesalahan")
	}
}
