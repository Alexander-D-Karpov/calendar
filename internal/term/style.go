package term

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

type Style struct {
	codes string
}

var (
	Plain   = Style{}
	Bold    = Style{"1"}
	Dim     = Style{"2"}
	Red     = Style{"31"}
	Green   = Style{"32"}
	Yellow  = Style{"33"}
	Blue    = Style{"34"}
	Magenta = Style{"35"}
	Cyan    = Style{"36"}
	Gray    = Style{"90"}
)

func (s Style) With(other Style) Style {
	switch {
	case s.codes == "":
		return other
	case other.codes == "":
		return s
	}
	return Style{s.codes + ";" + other.codes}
}

func (s Style) Render(text string, enabled bool) string {
	if !enabled || s.codes == "" || text == "" {
		return text
	}
	return "\x1b[" + s.codes + "m" + text + "\x1b[0m"
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func Strip(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

func VisibleWidth(s string) int {
	return utf8.RuneCountInString(Strip(s))
}

func PadRight(s string, width int) string {
	if n := VisibleWidth(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

func PadLeft(s string, width int) string {
	if n := VisibleWidth(s); n < width {
		return strings.Repeat(" ", width-n) + s
	}
	return s
}
