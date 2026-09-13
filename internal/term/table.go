package term

import (
	"io"
	"strings"
)

type Table struct {
	p      *Printer
	header []string
	right  map[int]bool
	rows   [][]string
	indent int
}

func (p *Printer) Table(header ...string) *Table {
	return &Table{p: p, header: header, right: map[int]bool{}}
}

func (t *Table) AlignRight(cols ...int) *Table {
	for _, c := range cols {
		t.right[c] = true
	}
	return t
}

func (t *Table) Indent(n int) *Table {
	t.indent = n
	return t
}

func (t *Table) Row(cells ...string) {
	t.rows = append(t.rows, cells)
}

func (t *Table) Len() int {
	return len(t.rows)
}

func (t *Table) Render() {
	cols := len(t.header)
	for _, r := range t.rows {
		cols = max(cols, len(r))
	}
	if cols == 0 {
		return
	}
	widths := make([]int, cols)
	measure := func(cells []string) {
		for i, c := range cells {
			widths[i] = max(widths[i], VisibleWidth(c))
		}
	}
	measure(t.header)
	for _, r := range t.rows {
		measure(r)
	}

	var b strings.Builder
	pad := strings.Repeat(" ", t.indent)
	if len(t.header) > 0 {
		header := make([]string, len(t.header))
		for i, h := range t.header {
			header[i] = t.p.Paint(Bold, h)
		}
		t.line(&b, pad, header, widths)
	}
	for _, r := range t.rows {
		t.line(&b, pad, r, widths)
	}
	io.WriteString(t.p.w, b.String())
}

func (t *Table) line(b *strings.Builder, pad string, cells []string, widths []int) {
	b.WriteString(pad)
	last := len(widths) - 1
	for i := range widths {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		if i > 0 {
			b.WriteString("  ")
		}
		switch {
		case t.right[i]:
			b.WriteString(PadLeft(cell, widths[i]))
		case i == last:
			b.WriteString(cell)
		default:
			b.WriteString(PadRight(cell, widths[i]))
		}
	}
	b.WriteByte('\n')
}
