package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Alexander-D-Karpov/calendar/internal/view"
)

func main() {
	out := flag.String("out", "web/static/css/slots.css", "output `path`")
	flag.Parse()
	if err := os.WriteFile(*out, view.GridCSS(), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
