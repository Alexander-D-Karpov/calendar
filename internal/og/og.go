package og

import (
	"bytes"
	"fmt"
	"image/color"
	"strings"

	"github.com/fogleman/gg"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"

	"github.com/Alexander-D-Karpov/calendar/internal/view"
)

const (
	Width  = 1200
	Height = 630

	headerHeight = 72
	pad          = 40
	minGridHours = 8

	dayLabelY = headerHeight + 18
	dayNumY   = headerHeight + 34
	allDayTop = headerHeight + 42
	allDayRow = 17
	maxAllDay = 2
	gridTop   = allDayTop + maxAllDay*allDayRow + 6
)

// The card is always light: it is read in a chat preview, which has its own
// background, not inside the app's theme.
var (
	bg      = color.RGBA{0xff, 0xff, 0xff, 0xff}
	fg      = color.RGBA{0x1f, 0x23, 0x28, 0xff}
	muted   = color.RGBA{0x59, 0x63, 0x6e, 0xff}
	lines   = color.RGBA{0xd1, 0xd9, 0xe0, 0xff}
	accent  = color.RGBA{0x2f, 0x61, 0x99, 0xff}
	nowLine = color.RGBA{0xb4, 0x23, 0x18, 0xff}
)

type Options struct {
	AppName string
	Name    string
	Title   string
	Kind    string
	TZ      string
	Counts  string
}

// Renderer holds the parsed fonts. A truetype.Font is immutable and safe to
// share, but a font.Face is not, so faces are built per render rather than
// cached here.
type Renderer struct {
	regular  *truetype.Font
	semiBold *truetype.Font
}

func New() (*Renderer, error) {
	reg, err := parse(regularFont)
	if err != nil {
		return nil, err
	}
	semi, err := parse(semiBoldFont)
	if err != nil {
		return nil, err
	}
	return &Renderer{regular: reg, semiBold: semi}, nil
}

func parse(name string) (*truetype.Font, error) {
	b, err := readFont(name)
	if err != nil {
		return nil, fmt.Errorf("og: %s: %w", name, err)
	}
	f, err := truetype.Parse(b)
	if err != nil {
		return nil, fmt.Errorf("og: %s: %w", name, err)
	}
	return f, nil
}

func (r *Renderer) face(f *truetype.Font, points float64) font.Face {
	return truetype.NewFace(f, &truetype.Options{Size: points, DPI: 72, Hinting: font.HintingFull})
}

func (r *Renderer) Render(p view.Period, items []view.Item, o view.Options, meta Options) ([]byte, error) {
	dc := gg.NewContext(Width, Height)
	dc.SetColor(bg)
	dc.Clear()

	r.header(dc, meta)

	body := view.Render(p, items, o)
	dc.Push()
	switch {
	case body.Grid != nil:
		r.grid(dc, body.Grid)
	case body.Month != nil:
		r.month(dc, body.Month)
	case body.Year != nil:
		r.year(dc, body.Year)
	case body.Agenda != nil:
		r.agenda(dc, body.Agenda)
	}
	dc.Pop()

	var buf bytes.Buffer
	if err := dc.EncodePNG(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (r *Renderer) header(dc *gg.Context, meta Options) {
	dc.SetFontFace(r.face(r.semiBold, 34))
	dc.SetColor(fg)
	name := clip(dc, meta.Name, Width-2*pad)
	dc.DrawString(name, pad, 46)

	parts := make([]string, 0, 4)
	for _, s := range []string{meta.Kind, meta.Title, meta.Counts} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	dc.SetFontFace(r.face(r.regular, 20))
	dc.SetColor(muted)
	dc.DrawString(clip(dc, strings.Join(parts, " · "), Width-2*pad), pad, headerHeight-4)

	dc.SetColor(lines)
	dc.SetLineWidth(1)
	dc.DrawLine(0, headerHeight, Width, headerHeight)
	dc.Stroke()
}

func (r *Renderer) grid(dc *gg.Context, g *view.Grid) {
	if len(g.Columns) == 0 {
		return
	}
	from, to := gridRows(g)
	rows := float64(to - from)
	top := float64(gridTop)
	bottom := float64(Height - pad/2)
	height := bottom - top
	gutter := 56.0
	colWidth := (Width - gutter - pad) / float64(len(g.Columns))
	rowHeight := height / rows

	dc.SetFontFace(r.face(r.regular, 16))
	for i, c := range g.Columns {
		x := gutter + float64(i)*colWidth
		dc.SetColor(muted)
		dc.DrawString(c.Weekday, x+6, dayLabelY)
		if c.Today {
			dc.SetColor(accent)
		} else {
			dc.SetColor(fg)
		}
		dc.DrawString(fmt.Sprint(c.Num), x+6, dayNumY)

		dc.SetColor(lines)
		dc.SetLineWidth(1)
		dc.DrawLine(x, top, x, bottom)
		dc.Stroke()
	}

	// Hour rules every two hours, labelled in the gutter.
	dc.SetFontFace(r.face(r.regular, 13))
	for row := from; row <= to; row += view.SlotsPerHour * 2 {
		y := top + float64(row-from)*rowHeight
		dc.SetColor(lines)
		dc.DrawLine(gutter, y, Width-pad, y)
		dc.Stroke()
		dc.SetColor(muted)
		dc.DrawStringAnchored(fmt.Sprintf("%02d:00", (row-1)/view.SlotsPerHour), gutter-8, y, 1, 0.35)
	}

	for i, c := range g.Columns {
		x := gutter + float64(i)*colWidth
		for _, band := range c.Sleep {
			r.band(dc, band, x, colWidth, top, rowHeight, from, to)
		}
		for _, b := range c.Blocks {
			r.block(dc, b, x, colWidth, top, rowHeight, from, to)
		}
		if c.NowRow > 0 && c.NowRow >= from && c.NowRow <= to {
			y := top + float64(c.NowRow-from)*rowHeight
			dc.SetColor(nowLine)
			dc.SetLineWidth(2)
			dc.DrawLine(x, y, x+colWidth, y)
			dc.Stroke()
		}
	}
	r.allDayBars(dc, g.Bars, gutter, colWidth, float64(allDayTop))
}

// gridRows crops to the busy part of the day so a card is not mostly empty
// night, while never showing less than eight hours.
func gridRows(g *view.Grid) (int, int) {
	first, last := -1, -1
	for _, c := range g.Columns {
		for _, b := range c.Blocks {
			if first < 0 || b.Row < first {
				first = b.Row
			}
			if end := b.Row + b.Span; end > last {
				last = end
			}
		}
	}
	scroll := g.ScrollHour * view.SlotsPerHour
	if first < 0 {
		first, last = scroll, scroll+minGridHours*view.SlotsPerHour
	}
	first = min(first, scroll)
	if span := minGridHours * view.SlotsPerHour; last-first < span {
		last = first + span
	}
	return max(first, 1), min(last, view.SlotsPerDay)
}

func (r *Renderer) block(dc *gg.Context, b view.Block, x, w, top, rowHeight float64, from, to int) {
	start, end := max(b.Row, from), min(b.Row+b.Span, to)
	if end <= start {
		return
	}
	y := top + float64(start-from)*rowHeight
	h := max(float64(end-start)*rowHeight, 14)
	lane := w / float64(max(b.Lanes, 1))
	bx := x + float64(b.Lane)*lane
	cr, cg, cb := blend(b.Color, 0.18)

	dc.SetRGB(cr, cg, cb)
	dc.DrawRoundedRectangle(bx+2, y+1, lane-4, h-2, 4)
	dc.Fill()

	hr, hg, hb := hexRGB(b.Color)
	dc.SetRGB(hr, hg, hb)
	dc.DrawRectangle(bx+2, y+1, 3, h-2)
	dc.Fill()

	dc.SetFontFace(r.face(r.semiBold, 14))
	dc.SetColor(fg)
	dc.DrawString(clip(dc, b.Title, lane-14), bx+10, y+14)
}

func (r *Renderer) band(dc *gg.Context, b view.Band, x, w, top, rowHeight float64, from, to int) {
	start, end := max(b.Row, from), min(b.Row+b.Span, to)
	if end <= start {
		return
	}
	y := top + float64(start-from)*rowHeight
	dc.SetRGBA(float64(muted.R)/255, float64(muted.G)/255, float64(muted.B)/255, 0.12)
	dc.DrawRectangle(x, y, w, float64(end-start)*rowHeight)
	dc.Fill()
}

func (r *Renderer) allDayBars(dc *gg.Context, bars []view.Bar, gutter, colWidth, y float64) {
	dc.SetFontFace(r.face(r.regular, 13))
	for i, b := range bars {
		if i >= maxAllDay {
			return
		}
		x := gutter + float64(b.Col)*colWidth
		w := float64(b.Span) * colWidth
		cr, cg, cb := blend(b.Color, 0.18)
		dc.SetRGB(cr, cg, cb)
		dc.DrawRoundedRectangle(x+2, y+float64(i)*allDayRow, w-4, 15, 3)
		dc.Fill()
		dc.SetColor(fg)
		dc.DrawString(clip(dc, b.Title, w-12), x+8, y+float64(i)*allDayRow+11)
	}
}

func (r *Renderer) month(dc *gg.Context, m *view.MonthView) {
	if len(m.Weeks) == 0 {
		return
	}
	top := float64(headerHeight + 28)
	cell := float64(Width-2*pad) / 7
	rowHeight := (float64(Height-pad) - top) / float64(len(m.Weeks))

	dc.SetFontFace(r.face(r.regular, 14))
	dc.SetColor(muted)
	for i, d := range m.Weekdays {
		dc.DrawString(d, pad+float64(i)*cell+6, top-8)
	}
	for wi, week := range m.Weeks {
		y := top + float64(wi)*rowHeight
		dc.SetColor(lines)
		dc.SetLineWidth(1)
		dc.DrawLine(pad, y, Width-pad, y)
		dc.Stroke()
		for _, d := range week.Days {
			x := pad + float64(d.Col-1)*cell
			dc.SetFontFace(r.face(r.semiBold, 15))
			switch {
			case d.Today:
				dc.SetColor(accent)
			case d.Outside:
				dc.SetColor(lines)
			default:
				dc.SetColor(fg)
			}
			dc.DrawString(fmt.Sprint(d.Num), x+6, y+18)
		}
		shown := 0
		dc.SetFontFace(r.face(r.regular, 13))
		for _, b := range week.Bars {
			if shown >= 3 {
				break
			}
			x := pad + float64(b.Col)*cell
			w := float64(b.Span) * cell
			by := y + 24 + float64(shown)*17
			cr, cg, cb := blend(b.Color, 0.18)
			dc.SetRGB(cr, cg, cb)
			dc.DrawRoundedRectangle(x+3, by, w-6, 15, 3)
			dc.Fill()
			dc.SetColor(fg)
			dc.DrawString(clip(dc, b.Title, w-14), x+9, by+11)
			shown++
		}
		if extra := len(week.Bars) - shown; extra > 0 {
			dc.SetColor(muted)
			dc.DrawString(fmt.Sprintf("+%d", extra), pad+6, y+24+float64(shown)*17+11)
		}
	}
}

func (r *Renderer) year(dc *gg.Context, y *view.YearView) {
	cols, rows := 4, 3
	cw := (Width - 2*pad) / float64(cols)
	ch := (float64(Height-pad) - float64(headerHeight+16)) / float64(rows)
	for i, m := range y.Months {
		if i >= cols*rows {
			break
		}
		ox := pad + float64(i%cols)*cw
		oy := float64(headerHeight+16) + float64(i/cols)*ch
		dc.SetFontFace(r.face(r.semiBold, 15))
		dc.SetColor(fg)
		dc.DrawString(m.Title, ox+4, oy+16)

		cell := min((cw-16)/7, (ch-34)/7)
		for di, d := range m.Days {
			if d.Outside {
				continue
			}
			cx := ox + 4 + float64(di%7)*cell
			cy := oy + 26 + float64(di/7)*cell
			// An empty day still gets a tile, so the block reads as a month
			// rather than as a few floating marks.
			if n := level(d.Count); n == 0 {
				dc.SetColor(lines)
			} else {
				ar, ag, ab := blend("2f6199", 0.2+0.2*float64(n))
				dc.SetRGB(ar, ag, ab)
			}
			dc.DrawRectangle(cx, cy, cell-2, cell-2)
			dc.Fill()
		}
	}
}

// level buckets a day's event count into the same five density steps the year
// grid uses in the browser.
func level(n int) int {
	switch {
	case n == 0:
		return 0
	case n < 2:
		return 1
	case n < 4:
		return 2
	case n < 7:
		return 3
	}
	return 4
}

func (r *Renderer) agenda(dc *gg.Context, a *view.AgendaView) {
	y := float64(headerHeight + 34)
	shown := 0
	for _, day := range a.Days {
		first := true
		for _, it := range day.Items {
			if shown >= 12 {
				return
			}
			dc.SetFontFace(r.face(r.regular, 15))
			dc.SetColor(muted)
			if first {
				dc.DrawString(day.Label, pad, y)
				first = false
			}
			dc.DrawString(it.TimeLabel, pad+190, y)

			hr, hg, hb := hexRGB(it.Color)
			dc.SetRGB(hr, hg, hb)
			dc.DrawRectangle(pad+300, y-11, 4, 14)
			dc.Fill()

			dc.SetFontFace(r.face(r.semiBold, 16))
			dc.SetColor(fg)
			dc.DrawString(clip(dc, it.Title, Width-pad-320), pad+312, y)
			y += 32
			shown++
		}
	}
}

// clip truncates on measured width rather than rune count, so a long title ends
// in an ellipsis instead of overflowing its box.
func clip(dc *gg.Context, s string, w float64) string {
	if w <= 0 {
		return ""
	}
	if width, _ := dc.MeasureString(s); width <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 {
		r = r[:len(r)-1]
		candidate := strings.TrimRight(string(r), " ") + "…"
		if width, _ := dc.MeasureString(candidate); width <= w {
			return candidate
		}
	}
	return ""
}

// blend approximates the CSS color-mix the browser applies to event blocks.
func blend(hex string, pct float64) (float64, float64, float64) {
	r, g, b := hexRGB(hex)
	return r*pct + (1 - pct), g*pct + (1 - pct), b*pct + (1 - pct)
}

func hexRGB(hex string) (float64, float64, float64) {
	h := view.Hex(hex)
	if len(h) != 6 {
		h = "3b6ea5"
	}
	var r, g, b int
	if _, err := fmt.Sscanf(h, "%02x%02x%02x", &r, &g, &b); err != nil {
		return 0.23, 0.43, 0.65
	}
	return float64(r) / 255, float64(g) / 255, float64(b) / 255
}
