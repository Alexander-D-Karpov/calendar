package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestResolveFormat(t *testing.T) {
	var buf bytes.Buffer
	cases := map[string]string{
		"":           FormatJSON,
		FormatAuto:   FormatJSON,
		FormatJSON:   FormatJSON,
		FormatText:   FormatText,
		FormatPretty: FormatPretty,
	}
	for in, want := range cases {
		if got := ResolveFormat(in, &buf); got != want {
			t.Errorf("ResolveFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAutoFormatWritesJSONToPipes(t *testing.T) {
	var buf bytes.Buffer
	New(Options{Format: FormatAuto, Writer: &buf}).Info("x", "k", 1)
	if !strings.HasPrefix(buf.String(), "{") {
		t.Fatalf("auto output to non-terminal = %q, want json", buf.String())
	}
}
