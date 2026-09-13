package web

import (
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Alexander-D-Karpov/calendar/internal/ratelimit"
)

type uaToken struct {
	token string
	name  string
}

var uaBrowsers = []uaToken{
	{"calendar-cli", "Calendar CLI"},
	{"curl/", "curl"},
	{"Edg/", "Edge"},
	{"OPR/", "Opera"},
	{"YaBrowser/", "Yandex Browser"},
	{"Firefox/", "Firefox"},
	{"Chrome/", "Chrome"},
	{"Safari/", "Safari"},
}

var uaSystems = []uaToken{
	{"Android", "Android"},
	{"iPhone", "iOS"},
	{"iPad", "iPadOS"},
	{"Windows", "Windows"},
	{"Mac OS X", "macOS"},
	{"CrOS", "ChromeOS"},
	{"Linux", "Linux"},
}

func describeUA(ua string) string {
	if ua == "" {
		return "Unknown device"
	}
	browser, system := matchUA(ua, uaBrowsers), matchUA(ua, uaSystems)
	switch {
	case browser != "" && system != "":
		return browser + " on " + system
	case browser != "":
		return browser
	case system != "":
		return system
	}
	if len(ua) > 48 {
		return strings.ToValidUTF8(ua[:48], "") + "…"
	}
	return ua
}

func matchUA(ua string, list []uaToken) string {
	for _, t := range list {
		if strings.Contains(ua, t.token) {
			return t.name
		}
	}
	return ""
}

func SafeNext(s string) string {
	if s == "" || s[0] != '/' || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/\\") || strings.ContainsAny(s, "\r\n\t\\") {
		return "/"
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/"
	}
	return s
}

func sentence(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	s = string(unicode.ToUpper(r)) + s[n:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

func humanWait(d time.Duration) string {
	s := ratelimit.Seconds(d)
	if s < 60 {
		return Plural(s, "second")
	}
	return Plural((s+59)/60, "minute")
}

func Plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
