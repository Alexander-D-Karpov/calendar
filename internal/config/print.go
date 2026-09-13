package config

import (
	"bufio"
	"fmt"
	"io"
)

type Entry struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Default bool   `json:"default"`
}

func (c *Config) Entries() []Entry {
	out := make([]Entry, len(c.entries))
	for i, e := range c.entries {
		out[i] = Entry(e)
	}
	return out
}

func (c *Config) Print(w io.Writer) error {
	bw := bufio.NewWriter(w)
	width := 0
	for _, e := range c.entries {
		width = max(width, len(e.Key))
	}
	for _, e := range c.entries {
		source := "set"
		if e.Default {
			source = "default"
		}
		fmt.Fprintf(bw, "%-*s  %-7s  %s\n", width, e.Key, source, e.Value)
	}
	return bw.Flush()
}
