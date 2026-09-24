package catalog

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReadBMKGLegacyCSV(t *testing.T) {
	in := "\xef\xbb\xbftgl,ot,lat,lon,depth,mag,remark\n" +
		"2022/11/21,06:21:09.874,-6.85,107.03,11,5.6,Java - Indonesia\n" +
		"2022/11/21,rusak,-6.85,107.03,11,5.6,x\n"
	got, skipped, err := ReadBMKG(strings.NewReader(in))
	if err != nil || skipped != 1 || len(got) != 1 {
		t.Fatalf("%+v %d %v", got, skipped, err)
	}
	want := time.Date(2022, 11, 21, 6, 21, 9, 874_000_000, time.UTC)
	if !got[0].OccurredAt.Equal(want) || got[0].Magnitude != 5.6 || got[0].Latitude != -6.85 || got[0].ID != "bmkg-v1-2" {
		t.Fatalf("%+v", got[0])
	}
}

func TestReadBMKGV2TSV(t *testing.T) {
	in := "eventID\tdatetime\tlatitude\tlongitude\tmagnitude\tmag_type\n" +
		"bmg2022wshn\t2022-11-21 06:21:09.874000+00:00\t-6.85\t107.03\t5.6\tMLv\n" +
		"x\t2022-11-21 06:21:09.874000+00:00\t-6.85\t\t5.6\tMLv\n"
	got, skipped, err := ReadBMKG(strings.NewReader(in))
	if err != nil || skipped != 1 || len(got) != 1 || got[0].ID != "bmg2022wshn" || got[0].Longitude != 107.03 {
		t.Fatalf("%+v %d %v", got, skipped, err)
	}
}

func TestReadUSGS(t *testing.T) {
	in := "time,latitude,longitude,depth,mag,magType,id,type\n" +
		"2022-11-21T06:21:07.066Z,-6.836,106.9968,10,5.6,mww,us7000ir9t,earthquake\n" +
		"2022-11-21T07:00:00.000Z,-6.8,107,0,2.6,ml,ex1,quarry blast\n"
	got, skipped, err := ReadUSGS(strings.NewReader(in))
	if err != nil || skipped != 1 || len(got) != 1 || got[0].ID != "us7000ir9t" || got[0].OccurredAt.Nanosecond() != 66_000_000 {
		t.Fatalf("%+v %d %v", got, skipped, err)
	}
}

func TestUnknownFormats(t *testing.T) {
	if _, _, err := ReadBMKG(strings.NewReader("a,b\n1,2\n")); !errors.Is(err, ErrFormat) {
		t.Errorf("BMKG: %v", err)
	}
	if _, _, err := ReadUSGS(strings.NewReader("a,b\n")); !errors.Is(err, ErrFormat) {
		t.Errorf("USGS: %v", err)
	}
	if _, _, err := ReadUSGS(strings.NewReader("")); err == nil {
		t.Error("file kosong")
	}
	if _, _, err := ReadBMKG(strings.NewReader("")); err == nil {
		t.Error("file kosong")
	}
	if _, _, err := ReadUSGS(strings.NewReader("time,latitude,longitude,mag,id\n\"a,b\n")); err == nil {
		t.Error("CSV rusak harus gagal")
	}
}
