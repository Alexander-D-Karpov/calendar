package web

import (
	"mime/multipart"
	"testing"
)

func TestSafeFilename(t *testing.T) {
	cases := map[string]string{
		"calendar.ics":            "calendar.ics",
		`C:\Users\sasha\work.ics`: "work.ics",
		"../../etc/passwd":        "passwd",
		"":                        "upload",
		"  ":                      "upload",
		"..":                      "upload",
		"tab\tname.csv":           "tabname.csv",
		`quote"name.ics`:          "quotename.ics",
		"экспорт.yaml":            "экспорт.yaml",
	}
	for in, want := range cases {
		if got := SafeFilename(&multipart.FileHeader{Filename: in}); got != want {
			t.Errorf("SafeFilename(%q) = %q, want %q", in, got, want)
		}
	}
	if SafeFilename(nil) != "upload" {
		t.Fatal("a missing header must fall back")
	}
}

func TestByteSize(t *testing.T) {
	cases := map[int64]string{0: "0B", 512: "512B", 1 << 10: "1KB", 20 << 20: "20MB", 2 << 30: "2GB"}
	for in, want := range cases {
		if got := ByteSize(in); got != want {
			t.Errorf("ByteSize(%d) = %q, want %q", in, got, want)
		}
	}
}
