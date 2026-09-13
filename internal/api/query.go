package api

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func queryBool(q url.Values, key string, def bool) (bool, error) {
	s := strings.TrimSpace(q.Get(key))
	if s == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		var v domain.ValidationError
		v.Add(key, "must be true or false")
		return false, v.Err()
	}
	return b, nil
}

func queryList(q url.Values, key string) []string {
	var out []string
	for _, raw := range q[key] {
		for _, s := range strings.Split(raw, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func queryInt(q url.Values, key string, def int) (int, error) {
	s := strings.TrimSpace(q.Get(key))
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		var v domain.ValidationError
		v.Add(key, "must be an integer")
		return 0, v.Err()
	}
	return n, nil
}
