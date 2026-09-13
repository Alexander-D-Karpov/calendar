package api

import (
	"net/http"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type sleepWindowJSON struct {
	Weekday int    `json:"weekday"`
	Start   string `json:"start"`
	End     string `json:"end"`
}

type sleepJSON struct {
	Enabled bool              `json:"enabled"`
	Windows []sleepWindowJSON `json:"windows"`
}

type meResponse struct {
	ID               string    `json:"id"`
	Email            string    `json:"email"`
	DisplayName      string    `json:"display_name"`
	Timezone         string    `json:"timezone"`
	EmailVerified    bool      `json:"email_verified"`
	WeekStart        int       `json:"week_start"`
	TimeFormat       string    `json:"time_format"`
	DefaultView      string    `json:"default_view"`
	DateOnlyReminder string    `json:"date_only_reminder"`
	Sleep            sleepJSON `json:"sleep"`
	CreatedAt        time.Time `json:"created_at"`
}

func toMe(u domain.User, ws []domain.SleepWindow) meResponse {
	sleep := sleepJSON{Enabled: u.SleepEnabled, Windows: make([]sleepWindowJSON, len(ws))}
	for i, w := range ws {
		sleep.Windows[i] = sleepWindowJSON{Weekday: int(w.Weekday), Start: domain.FormatClock(w.Start), End: domain.FormatClock(w.End)}
	}
	return meResponse{
		ID:               u.ID.String(),
		Email:            u.Email,
		DisplayName:      u.DisplayName,
		Timezone:         u.Timezone,
		EmailVerified:    u.Verified(),
		WeekStart:        u.WeekStart,
		TimeFormat:       u.TimeFormat,
		DefaultView:      u.DefaultView,
		DateOnlyReminder: domain.FormatClock(u.DateOnlyReminder),
		Sleep:            sleep,
		CreatedAt:        u.CreatedAt.UTC(),
	}
}

func getMe(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		u, ws, err := d.Settings.Get(r.Context(), ownerID(r))
		if err != nil {
			return err
		}
		writeResource(w, r, http.StatusOK, u.ETag(), toMe(u, ws))
		return nil
	}
}

func updateMe(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		var p domain.SettingsPatch
		if err := decodeJSON(r, &p, mediaMergePatch, mediaJSON); err != nil {
			return err
		}
		u, ws, err := d.Settings.Update(r.Context(), ownerID(r), p, r.Header.Get("If-Match"))
		if err != nil {
			return err
		}
		writeResource(w, r, http.StatusOK, u.ETag(), toMe(u, ws))
		return nil
	}
}
