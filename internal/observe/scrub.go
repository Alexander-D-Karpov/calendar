package observe

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/getsentry/sentry-go"
)

var (
	userinfoPattern = regexp.MustCompile(`((?:https?|wss?|webcal|postgres(?:ql)?|socks5h?)://)[^\s/@"'<>]+@`)
	queryPattern    = regexp.MustCompile(`((?:https?|wss?|webcal)://[^\s"'<>?#]+)\?[^\s"'<>#]*`)
	sharePattern    = regexp.MustCompile(`/s/[A-Za-z0-9_-]{8,}`)
	apiTokenPattern = regexp.MustCompile(`\bcal_[A-Za-z0-9]+_[A-Za-z0-9_-]+`)
	authPattern     = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]+`)
)

var allowedHeaders = map[string]bool{
	"Accept":          true,
	"Accept-Language": true,
	"Content-Length":  true,
	"Content-Type":    true,
	"Origin":          true,
	"Referer":         true,
	"Sec-Fetch-Mode":  true,
	"Sec-Fetch-Site":  true,
	"User-Agent":      true,
	"X-Request-Id":    true,
}

func ScrubString(s string) string {
	if s == "" {
		return s
	}
	s = userinfoPattern.ReplaceAllString(s, "${1}[redacted]@")
	s = queryPattern.ReplaceAllString(s, "${1}?[redacted]")
	s = sharePattern.ReplaceAllString(s, "/s/:redacted")
	s = apiTokenPattern.ReplaceAllString(s, "cal_[redacted]")
	return authPattern.ReplaceAllString(s, "$1 [redacted]")
}

func scrubURL(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	return ScrubString(u)
}

func Scrub(e *sentry.Event) *sentry.Event {
	if e == nil {
		return nil
	}
	e.Message = ScrubString(e.Message)
	e.Transaction = ScrubString(e.Transaction)
	for i := range e.Exception {
		e.Exception[i].Value = ScrubString(e.Exception[i].Value)
	}
	for _, c := range e.Contexts {
		scrubMap(c)
	}
	for k, v := range e.Tags {
		e.Tags[k] = ScrubString(v)
	}
	for _, b := range e.Breadcrumbs {
		scrubBreadcrumb(b)
	}
	if e.Request != nil {
		scrubRequest(e.Request)
	}
	e.User = sentry.User{ID: e.User.ID}
	return e
}

func scrubMap(m map[string]any) {
	for k, v := range m {
		switch x := v.(type) {
		case string:
			m[k] = ScrubString(x)
		case map[string]any:
			scrubMap(x)
		}
	}
}

func scrubRequest(r *sentry.Request) {
	r.URL = scrubURL(r.URL)
	r.QueryString = ""
	r.Cookies = ""
	r.Data = ""
	r.Env = nil
	headers := make(map[string]string, len(r.Headers))
	for k, v := range r.Headers {
		ck := http.CanonicalHeaderKey(k)
		if !allowedHeaders[ck] {
			continue
		}
		if ck == "Referer" || ck == "Origin" {
			v = scrubURL(v)
		}
		headers[ck] = ScrubString(v)
	}
	r.Headers = headers
}

func scrubBreadcrumb(b *sentry.Breadcrumb) {
	if b == nil {
		return
	}
	b.Message = ScrubString(b.Message)
	for k, v := range b.Data {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if k == "url" {
			b.Data[k] = scrubURL(s)
		} else {
			b.Data[k] = ScrubString(s)
		}
	}
}
