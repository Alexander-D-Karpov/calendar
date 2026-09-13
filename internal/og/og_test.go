package og

import (
	"bytes"
	"flag"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/view"
)

var update = flag.Bool("update", false, "write rendered PNGs to testdata for a visual check")

func fixture(t *testing.T, kind view.Kind) (view.Period, []view.Item, view.Options, Options) {
	t.Helper()
	start := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	items := []view.Item{
		{ID: "a", Title: "Standup with the platform team", Color: "3b6ea5", Start: start, End: start.Add(30 * time.Minute)},
		{ID: "b", Title: "Design review", Color: "aa3355", Start: start.Add(2 * time.Hour), End: start.Add(3 * time.Hour)},
		{ID: "c", Title: "Offsite", Color: "5b7c5a", AllDay: true, Start: view.Date(start), End: view.Date(start).AddDate(0, 0, 2)},
		{ID: "d", Title: "Pay rent", Color: "c98b2e", Todo: true, AllDay: true, Start: view.Date(start), End: view.Date(start).AddDate(0, 0, 1)},
	}
	o := view.Options{
		Loc: time.UTC, Now: start, Clock24: true,
		Sleep: []domain.SleepWindow{{Weekday: time.Thursday, Start: 23 * 60, End: 7 * 60}},
	}
	meta := Options{AppName: "Calendar", Name: "Work week", Title: "7 – 13 September 2026", Kind: kind.Label(), Counts: "4 events"}
	return view.NewPeriod(kind, start, time.Monday), items, o, meta
}

func render(t *testing.T, r *Renderer, kind view.Kind) []byte {
	t.Helper()
	p, items, o, meta := fixture(t, kind)
	b, err := r.Render(p, items, o, meta)
	if err != nil {
		t.Fatalf("%s: %v", kind, err)
	}
	return b
}

func newRenderer(t *testing.T) *Renderer {
	t.Helper()
	r, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRenderSize(t *testing.T) {
	r := newRenderer(t)
	for _, kind := range view.Kinds {
		body := render(t, r, kind)
		cfg, format, err := image.DecodeConfig(bytes.NewReader(body))
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if format != "png" {
			t.Errorf("%s: format = %q", kind, format)
		}
		if cfg.Width != Width || cfg.Height != Height {
			t.Errorf("%s: %dx%d, want %dx%d", kind, cfg.Width, cfg.Height, Width, Height)
		}
	}
}

// Map iteration order leaking into draw order would make the same input render
// differently between calls, which breaks caching by content.
func TestRenderIsDeterministic(t *testing.T) {
	r := newRenderer(t)
	for _, kind := range view.Kinds {
		first := render(t, r, kind)
		second := render(t, r, kind)
		if !bytes.Equal(first, second) {
			t.Errorf("%s: two renders of the same input differ", kind)
		}
	}
}

// A font.Face is not safe for concurrent use. If faces were cached on the
// Renderer this would produce garbled or differing output under load.
func TestRenderIsConcurrencySafe(t *testing.T) {
	r := newRenderer(t)
	want := render(t, r, view.Week)
	p, items, o, meta := fixture(t, view.Week)

	const n = 8
	out := make([][]byte, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, err := r.Render(p, items, o, meta)
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
				return
			}
			out[i] = b
		}()
	}
	wg.Wait()
	for i, got := range out {
		if !bytes.Equal(got, want) {
			t.Errorf("goroutine %d rendered different bytes", i)
		}
	}
}

// A busy share must not leak titles. Redaction happens before rendering, so the
// proof is that the two produce different pixels and the busy one is not blank.
func TestRenderRedactsBusyDetail(t *testing.T) {
	r := newRenderer(t)
	p, items, o, meta := fixture(t, view.Week)

	full, err := r.Render(p, view.Redact(items, domain.DetailFull), o, meta)
	if err != nil {
		t.Fatal(err)
	}
	busy, err := r.Render(p, view.Redact(items, domain.DetailBusy), o, meta)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(full, busy) {
		t.Fatal("a busy share must not render the same pixels as a full one")
	}
	if ratio := inkRatio(t, busy); ratio < 0.005 {
		t.Errorf("the busy card is blank: only %.4f of pixels are drawn", ratio)
	}
}

// inkRatio is the share of pixels that differ from the background, which tells
// a genuinely empty render apart from one that merely hides titles.
func inkRatio(t *testing.T, body []byte) float64 {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	drawn := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r>>8 != 0xff || g>>8 != 0xff || bl>>8 != 0xff {
				drawn++
			}
		}
	}
	return float64(drawn) / float64(b.Dx()*b.Dy())
}

// TestWriteTestdata is not an assertion: run with -update to look at the cards.
func TestWriteTestdata(t *testing.T) {
	if !*update {
		t.Skip("run with -update to write PNGs")
	}
	r := newRenderer(t)
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	for _, kind := range view.Kinds {
		path := filepath.Join("testdata", string(kind)+".png")
		if err := os.WriteFile(path, render(t, r, kind), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", path)
	}
}
