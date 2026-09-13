package markdown

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	out := string(Render("**bold** <script>alert(1)</script> [x](javascript:alert(1)) [ok](https://example.com)"))
	for _, want := range []string{"<strong>bold</strong>", `href="https://example.com"`, "noreferrer"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	for _, bad := range []string{"<script", "javascript:"} {
		if strings.Contains(out, bad) {
			t.Errorf("unsafe %q in %s", bad, out)
		}
	}
	if Render("  ") != "" {
		t.Error("blank input must render nothing")
	}
}

func TestPlain(t *testing.T) {
	if got := Plain("# Title\n\n**Agenda** and [notes](https://x.test)", 100); got != "Title Agenda and notes" {
		t.Fatalf("Plain = %q", got)
	}
	if got := Plain("абвгдеёжз", 4); got != "абвг…" {
		t.Fatalf("truncated = %q", got)
	}
}
