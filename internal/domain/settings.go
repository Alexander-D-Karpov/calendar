package domain

const (
	TimeFormat12 = "12h"
	TimeFormat24 = "24h"
)

var (
	TimeFormats = []string{TimeFormat12, TimeFormat24}
	Views       = []string{"day", "3day", "week", "month", "year", "agenda"}
)

type SleepWindowInput struct {
	Weekday int    `json:"weekday"`
	Start   string `json:"start"`
	End     string `json:"end"`
}

type SleepInput struct {
	Enabled bool               `json:"enabled"`
	Windows []SleepWindowInput `json:"windows"`
}

type SettingsPatch struct {
	DisplayName      Opt[string]     `json:"display_name"`
	Timezone         Opt[string]     `json:"timezone"`
	WeekStart        Opt[int]        `json:"week_start"`
	TimeFormat       Opt[string]     `json:"time_format"`
	DefaultView      Opt[string]     `json:"default_view"`
	DedupPolicy      Opt[string]     `json:"dedup_policy"`
	DateOnlyReminder Opt[string]     `json:"date_only_reminder"`
	Sleep            Opt[SleepInput] `json:"sleep"`
}
