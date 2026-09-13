package service

import (
	"context"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func indexByID[T any](items []T, key func(T) (domain.ID, bool)) (map[domain.ID]T, T) {
	m := make(map[domain.ID]T, len(items))
	var def T
	found := false
	for _, it := range items {
		id, isDefault := key(it)
		m[id] = it
		if isDefault || !found {
			def, found = it, true
		}
	}
	return m, def
}

func userZone(ctx context.Context, users UserRepo, owner domain.ID) (string, *time.Location, error) {
	u, err := users.UserByID(ctx, owner)
	if err != nil {
		return "", nil, err
	}
	if u.Timezone == "" {
		return "UTC", time.UTC, nil
	}
	loc, err := time.LoadLocation(u.Timezone)
	if err != nil {
		return "UTC", time.UTC, nil
	}
	return u.Timezone, loc, nil
}

func applyNamed(v *domain.ValidationError, name, color domain.Opt[string], position domain.Opt[int], n, c *string, pos *int) {
	if x, ok := name.Value(v, "name"); ok {
		*n = strings.TrimSpace(x)
	}
	if x, ok := color.Value(v, "color"); ok {
		if col, valid := domain.NormalizeColor(x); valid {
			*c = col
		} else {
			v.Add("color", "must be a hex color like #3b6ea5")
		}
	}
	if x, ok := position.Value(v, "position"); ok {
		if x < 0 || x > domain.MaxCalendarPosition {
			v.Addf("position", "must be between 0 and %d", domain.MaxCalendarPosition)
		} else {
			*pos = x
		}
	}
}
