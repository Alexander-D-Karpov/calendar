package google

import (
	"slices"
	"testing"
)

func TestNotesRoundTrip(t *testing.T) {
	checks := []Check{{Text: "Riser cables", Done: true}, {Text: "Thermal paste"}}
	notes := JoinNotes("Check PSU rails\n", checks)
	if notes != "Check PSU rails\n\n---\n[x] Riser cables\n[ ] Thermal paste" {
		t.Fatalf("notes = %q", notes)
	}
	body, got := SplitNotes(notes)
	if body != "Check PSU rails" || !slices.Equal(got, checks) {
		t.Fatalf("split = %q %v", body, got)
	}
	if b, c := SplitNotes("a\n---\nnot a check"); b != "a\n---\nnot a check" || c != nil {
		t.Fatalf("malformed = %q %v", b, c)
	}
	if b, c := SplitNotes("---\n[X] only"); b != "" || len(c) != 1 || !c[0].Done {
		t.Fatalf("checks only = %q %v", b, c)
	}
	if JoinNotes("plain", nil) != "plain" {
		t.Fatal("no checks must keep the body")
	}
}

func TestDescriptionToMarkdown(t *testing.T) {
	in := `<b>Agenda</b><br>See <a href="https://x.test/a?b=1&amp;c=2">notes</a> &amp; <a href="https://y.test">https://y.test</a><ul><li>one</li><li>two</li></ul>`
	want := "**Agenda**\nSee [notes](https://x.test/a?b=1&c=2) & https://y.test- one\n- two"
	if got := DescriptionToMarkdown(in); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if got := DescriptionToMarkdown("a < b & c"); got != "a < b & c" {
		t.Fatalf("plain = %q", got)
	}
}
