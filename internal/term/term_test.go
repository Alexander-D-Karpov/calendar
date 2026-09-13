package term

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseColorMode(t *testing.T) {
	cases := map[string]ColorMode{
		"":       ColorAuto,
		"auto":   ColorAuto,
		"Always": ColorAlways,
		"on":     ColorAlways,
		"never":  ColorNever,
		"off":    ColorNever,
	}
	for in, want := range cases {
		got, err := ParseColorMode(in)
		if err != nil || got != want {
			t.Errorf("ParseColorMode(%q) = %v, %v, want %v", in, got, err, want)
		}
	}
	if _, err := ParseColorMode("rainbow"); err == nil {
		t.Error("expected error for invalid mode")
	}
	if ColorNever.String() != "never" || ColorAlways.String() != "always" || ColorAuto.String() != "auto" {
		t.Error("String() mismatch")
	}
}

func TestColorEnabled(t *testing.T) {
	var buf bytes.Buffer
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("TERM", "xterm-256color")

	if ColorEnabled(&buf, ColorAuto) {
		t.Error("auto must be off for non terminals")
	}
	if !ColorEnabled(&buf, ColorAlways) {
		t.Error("always must be on")
	}
	t.Setenv("FORCE_COLOR", "1")
	if !ColorEnabled(&buf, ColorAuto) {
		t.Error("FORCE_COLOR must enable color")
	}
	t.Setenv("NO_COLOR", "1")
	if ColorEnabled(&buf, ColorAuto) {
		t.Error("NO_COLOR must win over FORCE_COLOR")
	}
	if !ColorEnabled(&buf, ColorAlways) {
		t.Error("explicit always must override NO_COLOR")
	}
	if ColorEnabled(&buf, ColorNever) {
		t.Error("never must be off")
	}
}

func TestStyles(t *testing.T) {
	if got := Red.Render("x", true); got != "\x1b[31mx\x1b[0m" {
		t.Errorf("Render = %q", got)
	}
	if got := Red.With(Bold).Render("x", true); got != "\x1b[31;1mx\x1b[0m" {
		t.Errorf("With = %q", got)
	}
	if Plain.With(Dim) != Dim || Dim.With(Plain) != Dim {
		t.Error("With must ignore Plain")
	}
	if Red.Render("x", false) != "x" || Plain.Render("x", true) != "x" || Red.Render("", true) != "" {
		t.Error("Render must be a no-op when disabled, plain or empty")
	}
	colored := Green.Render("héllo", true)
	if Strip(colored) != "héllo" || VisibleWidth(colored) != 5 {
		t.Errorf("Strip/VisibleWidth mismatch for %q", colored)
	}
	if PadRight(colored, 7) != colored+"  " || PadLeft("ab", 4) != "  ab" || PadRight("abcdef", 3) != "abcdef" {
		t.Error("padding mismatch")
	}
}

func TestStatusLines(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinter(&buf, ColorNever)
	p.OK("migrated %d", 3)
	p.Info("note")
	p.Warn("careful")
	p.Error("failed: %s", "boom")
	p.Detail("more")
	want := "ok    migrated 3\n" +
		"info  note\n" +
		"warn  careful\n" +
		"error failed: boom\n" +
		"      more\n"
	if buf.String() != want {
		t.Fatalf("got\n%q\nwant\n%q", buf.String(), want)
	}
}

func TestFields(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinter(&buf, ColorNever)
	p.Fields(2, Field{"env", "dev"}, Field{"base url", "http://x"})
	want := "  env" + strings.Repeat(" ", 7) + "dev\n" + "  base url  http://x\n"
	if buf.String() != want {
		t.Fatalf("got %q want %q", buf.String(), want)
	}
}

func TestTable(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinter(&buf, ColorNever)
	tbl := p.Table("NAME", "COUNT").AlignRight(1)
	tbl.Row("alpha", "1")
	tbl.Row("b", "200")
	tbl.Render()
	want := "NAME   COUNT\n" +
		"alpha      1\n" +
		"b        200\n"
	if buf.String() != want {
		t.Fatalf("got\n%q\nwant\n%q", buf.String(), want)
	}
	if tbl.Len() != 2 {
		t.Errorf("Len() = %d", tbl.Len())
	}
}

func TestTableColoredWidths(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinter(&buf, ColorAlways)
	tbl := p.Table("K", "V")
	tbl.Row(p.Paint(Red, "ab"), "1")
	tbl.Row("abcd", "2")
	tbl.Render()
	want := "K     V\n" +
		"ab    1\n" +
		"abcd  2\n"
	if got := Strip(buf.String()); got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

func TestTableIndentNoHeader(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinter(&buf, ColorNever)
	tbl := p.Table().Indent(2)
	tbl.Row("up", "apply")
	tbl.Render()
	if buf.String() != "  up  apply\n" {
		t.Fatalf("got %q", buf.String())
	}
	var empty bytes.Buffer
	NewPrinter(&empty, ColorNever).Table().Render()
	if empty.Len() != 0 {
		t.Fatal("empty table must render nothing")
	}
}
