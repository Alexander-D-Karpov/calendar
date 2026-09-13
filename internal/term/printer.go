package term

import (
	"fmt"
	"io"
	"strings"
)

const labelWidth = 5

type Field struct {
	Key   string
	Value string
}

type Printer struct {
	w     io.Writer
	color bool
}

func NewPrinter(w io.Writer, mode ColorMode) *Printer {
	return &Printer{w: w, color: ColorEnabled(w, mode)}
}

func (p *Printer) Writer() io.Writer {
	return p.w
}

func (p *Printer) Color() bool {
	return p.color
}

func (p *Printer) Paint(s Style, text string) string {
	return s.Render(text, p.color)
}

func (p *Printer) Printf(format string, args ...any) {
	fmt.Fprintf(p.w, format, args...)
}

func (p *Printer) Println(args ...any) {
	fmt.Fprintln(p.w, args...)
}

func (p *Printer) Newline() {
	fmt.Fprintln(p.w)
}

func (p *Printer) Heading(text string) {
	fmt.Fprintln(p.w, p.Paint(Bold, text))
}

func (p *Printer) Hint(format string, args ...any) {
	fmt.Fprintln(p.w, p.Paint(Dim, fmt.Sprintf(format, args...)))
}

func (p *Printer) OK(format string, args ...any) {
	p.status(Green.With(Bold), "ok", format, args)
}

func (p *Printer) Info(format string, args ...any) {
	p.status(Cyan.With(Bold), "info", format, args)
}

func (p *Printer) Warn(format string, args ...any) {
	p.status(Yellow.With(Bold), "warn", format, args)
}

func (p *Printer) Error(format string, args ...any) {
	p.status(Red.With(Bold), "error", format, args)
}

func (p *Printer) Detail(format string, args ...any) {
	fmt.Fprintf(p.w, "%s %s\n", strings.Repeat(" ", labelWidth), fmt.Sprintf(format, args...))
}

func (p *Printer) Fields(indent int, fields ...Field) {
	width := 0
	for _, f := range fields {
		width = max(width, VisibleWidth(f.Key))
	}
	pad := strings.Repeat(" ", indent)
	for _, f := range fields {
		fmt.Fprintf(p.w, "%s%s  %s\n", pad, p.Paint(Dim, PadRight(f.Key, width)), f.Value)
	}
}

func (p *Printer) status(style Style, label, format string, args []any) {
	fmt.Fprintf(p.w, "%s %s\n", p.Paint(style, PadRight(label, labelWidth)), fmt.Sprintf(format, args...))
}
