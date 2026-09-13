package logging

import (
	"log/slog"
	"strings"
)

type Field struct {
	Key string
	Val slog.Value
}

func Flatten(dst []Field, prefix string, a slog.Attr) []Field {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return dst
	}
	if a.Value.Kind() == slog.KindGroup {
		p := JoinKey(prefix, a.Key)
		for _, ga := range a.Value.Group() {
			dst = Flatten(dst, p, ga)
		}
		return dst
	}
	return append(dst, Field{Key: JoinKey(prefix, a.Key), Val: a.Value})
}

func JoinKey(prefix, key string) string {
	switch {
	case prefix == "":
		return key
	case key == "":
		return prefix
	}
	return prefix + "." + key
}

func LastKey(key string) string {
	if i := strings.LastIndexByte(key, '.'); i >= 0 {
		return key[i+1:]
	}
	return key
}
