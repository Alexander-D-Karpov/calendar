package domain

import "time"

type SleepWindow struct {
	Weekday time.Weekday
	Start   int
	End     int
}
