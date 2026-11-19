package kiosk

import (
	"testing"
	"time"

	"github.com/XtremeOwnage/pikiosk/internal/config"
)

func at(day time.Weekday, hhmm string) time.Time {
	// 2026-11-15 is a Sunday.
	base := time.Date(2026, 11, 15, 0, 0, 0, 0, time.Local)
	m, _ := config.ParseClock(hhmm)
	return base.AddDate(0, 0, int(day)).Add(time.Duration(m) * time.Minute)
}

func TestInOffWindow(t *testing.T) {
	overnight := config.Sleep{ScheduleOff: "22:00", ScheduleOn: "07:00"}
	daytime := config.Sleep{ScheduleOff: "09:00", ScheduleOn: "17:00"}
	weeknights := config.Sleep{ScheduleOff: "22:00", ScheduleOn: "07:00", ScheduleWeekdays: []int{1, 2, 3, 4, 5}}

	cases := []struct {
		name string
		s    config.Sleep
		now  time.Time
		want bool
	}{
		{"overnight before", overnight, at(time.Monday, "21:59"), false},
		{"overnight start", overnight, at(time.Monday, "22:00"), true},
		{"overnight after midnight", overnight, at(time.Tuesday, "03:00"), true},
		{"overnight end", overnight, at(time.Tuesday, "07:00"), false},
		{"daytime inside", daytime, at(time.Wednesday, "12:00"), true},
		{"daytime outside", daytime, at(time.Wednesday, "18:00"), false},
		{"weeknight friday night", weeknights, at(time.Friday, "23:00"), true},
		{"weeknight saturday morning belongs to friday", weeknights, at(time.Saturday, "06:00"), true},
		{"weeknight saturday night", weeknights, at(time.Saturday, "23:00"), false},
		{"weeknight monday morning belongs to sunday", weeknights, at(time.Monday, "06:00"), false},
		{"equal times disable", config.Sleep{ScheduleOff: "07:00", ScheduleOn: "07:00"}, at(time.Monday, "07:00"), false},
	}
	for _, c := range cases {
		if got := InOffWindow(c.s, c.now); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCheckURL(t *testing.T) {
	if err := checkURL("https://ha.lan/dash", "http", "https"); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{"file:///etc/passwd", "javascript:alert(1)", "https://", "ha.lan"} {
		if checkURL(bad, "http", "https") == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
