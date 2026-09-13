package service

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type SettingsRepo interface {
	UserByID(ctx context.Context, id domain.ID) (domain.User, error)
	SleepSchedule(ctx context.Context, user domain.ID) ([]domain.SleepWindow, error)
	UpdateSettings(ctx context.Context, id domain.ID, fn func(*domain.User, *[]domain.SleepWindow) error) (domain.User, []domain.SleepWindow, error)
}

type Settings struct {
	repo SettingsRepo
}

func NewSettings(repo SettingsRepo) *Settings {
	return &Settings{repo: repo}
}

func (s *Settings) Get(ctx context.Context, id domain.ID) (domain.User, []domain.SleepWindow, error) {
	u, err := s.repo.UserByID(ctx, id)
	if err != nil {
		return domain.User{}, nil, err
	}
	ws, err := s.repo.SleepSchedule(ctx, id)
	return u, ws, err
}

func (s *Settings) Update(ctx context.Context, id domain.ID, p domain.SettingsPatch, ifMatch string) (domain.User, []domain.SleepWindow, error) {
	return s.repo.UpdateSettings(ctx, id, func(u *domain.User, ws *[]domain.SleepWindow) error {
		if err := domain.CheckIfMatch(ifMatch, u.ETag()); err != nil {
			return err
		}
		return applySettings(u, ws, p)
	})
}

func applySettings(u *domain.User, ws *[]domain.SleepWindow, p domain.SettingsPatch) error {
	var v domain.ValidationError
	if x, ok := p.DisplayName.Value(&v, "display_name"); ok {
		u.DisplayName = strings.TrimSpace(x)
	}
	if x, ok := p.Timezone.Value(&v, "timezone"); ok {
		if tz := strings.TrimSpace(x); domain.ValidTimezone(tz) {
			u.Timezone = tz
		} else {
			v.Add("timezone", "unknown timezone")
		}
	}
	if x, ok := p.WeekStart.Value(&v, "week_start"); ok {
		if x < 0 || x > 6 {
			v.Add("week_start", "must be between 0 for Sunday and 6 for Saturday")
		} else {
			u.WeekStart = x
		}
	}
	choice(&v, p.TimeFormat, "time_format", &u.TimeFormat, domain.TimeFormats...)
	choice(&v, p.DefaultView, "default_view", &u.DefaultView, domain.Views...)
	choice(&v, p.DedupPolicy, "dedup_policy", &u.DedupPolicy, domain.DedupPolicies...)
	if raw, ok := p.DateOnlyReminder.Value(&v, "date_only_reminder"); ok {
		if m, ok := domain.ParseClock(raw); ok {
			u.DateOnlyReminder = m
		} else {
			v.Add("date_only_reminder", "must be a time like 09:00")
		}
	}
	if x, ok := p.Sleep.Value(&v, "sleep"); ok {
		u.SleepEnabled = x.Enabled
		*ws = sleepWindows(&v, x.Windows)
	}
	v.Length("display_name", u.DisplayName, 0, 100)
	if u.SleepEnabled && len(*ws) == 0 && !hasField(&v, "sleep") {
		v.Add("sleep", "needs at least one window when enabled")
	}
	return v.Err()
}

func sleepWindows(v *domain.ValidationError, in []domain.SleepWindowInput) []domain.SleepWindow {
	out := make([]domain.SleepWindow, 0, len(in))
	seen := map[int]bool{}
	for i, w := range in {
		start, sok := domain.ParseClock(w.Start)
		end, eok := domain.ParseClock(w.End)
		switch {
		case w.Weekday < 0 || w.Weekday > 6:
			v.Addf("sleep", "window %d: weekday must be between 0 and 6", i+1)
		case seen[w.Weekday]:
			v.Addf("sleep", "window %d: weekday %d is listed twice", i+1, w.Weekday)
		case !sok || !eok:
			v.Addf("sleep", "window %d: start and end must be times like 23:30", i+1)
		case start == end:
			v.Addf("sleep", "window %d: start and end must differ", i+1)
		default:
			seen[w.Weekday] = true
			out = append(out, domain.SleepWindow{Weekday: time.Weekday(w.Weekday), Start: start, End: end})
		}
	}
	slices.SortFunc(out, func(a, b domain.SleepWindow) int { return int(a.Weekday) - int(b.Weekday) })
	return out
}
