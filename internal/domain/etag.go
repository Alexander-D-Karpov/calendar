package domain

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func TimeTag(t time.Time) string {
	return `"` + strconv.FormatInt(t.UnixMicro(), 36) + `"`
}

func VersionTag(n int64) string {
	return `"v` + strconv.FormatInt(n, 36) + `"`
}

func MatchETag(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	want := strings.TrimPrefix(etag, "W/")
	for _, part := range strings.Split(header, ",") {
		if strings.TrimPrefix(strings.TrimSpace(part), "W/") == want {
			return true
		}
	}
	return false
}

func CheckIfMatch(header, etag string) error {
	if strings.TrimSpace(header) == "" || MatchETag(header, etag) {
		return nil
	}
	return fmt.Errorf("%w: resource was modified, reload it and retry", ErrPrecondition)
}
