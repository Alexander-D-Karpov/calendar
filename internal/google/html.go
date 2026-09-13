package google

import (
	"html"
	"regexp"
	"strings"
)

var (
	htmlTag      = regexp.MustCompile(`<[a-zA-Z/!][^>]*>`)
	htmlBreak    = regexp.MustCompile(`(?i)<br\s*/?>`)
	htmlBlockEnd = regexp.MustCompile(`(?i)</(p|div|li|h[1-6]|tr|ul|ol)>`)
	htmlItem     = regexp.MustCompile(`(?i)<li[^>]*>`)
	htmlLink     = regexp.MustCompile(`(?is)<a\s[^>]*href\s*=\s*"([^"]*)"[^>]*>(.*?)</a>`)
	htmlBold     = regexp.MustCompile(`(?is)<(?:b|strong)>(.*?)</(?:b|strong)>`)
	htmlItalic   = regexp.MustCompile(`(?is)<(?:i|em)>(.*?)</(?:i|em)>`)
	blankLines   = regexp.MustCompile(`\n{3,}`)
)

func DescriptionToMarkdown(s string) string {
	if !htmlTag.MatchString(s) {
		return s
	}
	s = htmlLink.ReplaceAllStringFunc(s, func(m string) string {
		sub := htmlLink.FindStringSubmatch(m)
		href := html.UnescapeString(sub[1])
		text := strings.TrimSpace(html.UnescapeString(htmlTag.ReplaceAllString(sub[2], "")))
		if text == "" || text == href {
			return href
		}
		return "[" + text + "](" + href + ")"
	})
	s = htmlBold.ReplaceAllString(s, "**$1**")
	s = htmlItalic.ReplaceAllString(s, "*$1*")
	s = htmlBreak.ReplaceAllString(s, "\n")
	s = htmlBlockEnd.ReplaceAllString(s, "\n")
	s = htmlItem.ReplaceAllString(s, "- ")
	s = html.UnescapeString(htmlTag.ReplaceAllString(s, ""))
	return strings.TrimSpace(blankLines.ReplaceAllString(s, "\n\n"))
}
