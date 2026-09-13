package google

import (
	"regexp"
	"strings"
)

const notesSeparator = "---"

var checkLine = regexp.MustCompile(`^\[([ xX])\] (.*\S.*)$`)

type Check struct {
	Text string
	Done bool
}

func JoinNotes(body string, checks []Check) string {
	body = strings.TrimRight(body, " \t\r\n")
	if len(checks) == 0 {
		return body
	}
	var b strings.Builder
	if body != "" {
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	b.WriteString(notesSeparator)
	for _, c := range checks {
		mark := " "
		if c.Done {
			mark = "x"
		}
		b.WriteString("\n[" + mark + "] " + strings.Join(strings.Fields(c.Text), " "))
	}
	return b.String()
}

func SplitNotes(notes string) (string, []Check) {
	lines := strings.Split(strings.ReplaceAll(notes, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != notesSeparator {
			continue
		}
		var checks []Check
		for _, l := range lines[i+1:] {
			l = strings.TrimSpace(l)
			if l == "" {
				continue
			}
			m := checkLine.FindStringSubmatch(l)
			if m == nil {
				return notes, nil
			}
			checks = append(checks, Check{Text: strings.TrimSpace(m[2]), Done: m[1] != " "})
		}
		if len(checks) == 0 {
			return notes, nil
		}
		return strings.TrimRight(strings.Join(lines[:i], "\n"), " \t\n"), checks
	}
	return notes, nil
}
