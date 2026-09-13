package term

import (
	"fmt"
	"io"
	"os"
	"strings"

	xterm "golang.org/x/term"
)

type ColorMode int

const (
	ColorAuto ColorMode = iota
	ColorAlways
	ColorNever
)

func ParseColorMode(s string) (ColorMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return ColorAuto, nil
	case "always", "on", "yes", "true", "force":
		return ColorAlways, nil
	case "never", "off", "no", "false", "none":
		return ColorNever, nil
	}
	return ColorAuto, fmt.Errorf("invalid color mode %q, expected auto, always or never", s)
}

func (m ColorMode) String() string {
	switch m {
	case ColorAlways:
		return "always"
	case ColorNever:
		return "never"
	}
	return "auto"
}

func IsTerminal(v any) bool {
	f, ok := v.(interface{ Fd() uintptr })
	return ok && xterm.IsTerminal(int(f.Fd()))
}

func ColorEnabled(w io.Writer, mode ColorMode) bool {
	switch mode {
	case ColorAlways:
		return true
	case ColorNever:
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if v := os.Getenv("FORCE_COLOR"); v != "" && v != "0" && v != "false" {
		return true
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return IsTerminal(w)
}
