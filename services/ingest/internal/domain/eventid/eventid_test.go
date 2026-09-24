package eventid

import "testing"

func TestMsgID(t *testing.T) {
	a := MsgID("bmkg-autogempa", "20260923121656", []byte("isi"))
	if len(a) != 32 {
		t.Fatalf("panjang %d, ingin 32", len(a))
	}
	if a != MsgID("bmkg-autogempa", "20260923121656", []byte("isi")) {
		t.Fatal("tidak deterministik")
	}
	for _, other := range []string{
		MsgID("bmkg-gempaterkini", "20260923121656", []byte("isi")),
		MsgID("bmkg-autogempa", "20260923121657", []byte("isi")),
		MsgID("bmkg-autogempa", "20260923121656", []byte("isi baru")),
		MsgID("bmkg-autogempa2", "0260923121656", []byte("isi")), // geser batas antarbagian
	} {
		if other == a {
			t.Fatalf("ID bertabrakan untuk masukan berbeda: %s", a)
		}
	}
}
