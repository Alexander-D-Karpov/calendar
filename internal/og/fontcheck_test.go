package og

import (
	"testing"

	"github.com/golang/freetype/truetype"
)

func TestFontsParse(t *testing.T) {
	for _, n := range []string{regularFont, semiBoldFont} {
		b, err := readFont(n)
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		f, err := truetype.Parse(b)
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if f.Index('A') == 0 {
			t.Errorf("%s has no glyph for A", n)
		}
	}
}
