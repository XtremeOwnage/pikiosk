package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	s, err := Open(p, "kiosk_abc123")
	if err != nil {
		t.Fatal(err)
	}
	if s.Get().APIToken == "" {
		t.Fatal("no API token generated")
	}
	called := false
	s.Subscribe(func(old, cfg Config) { called = len(cfg.Views) == 1 && len(old.Views) == 0 })
	err = s.Update(func(c *Config) error {
		c.Views = []View{{Name: "ha", URL: "https://ha.lan"}}
		c.DefaultView = "ha"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("subscriber not notified")
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("config mode %v, want 0600", st.Mode().Perm())
	}

	s2, err := Open(p, "other")
	if err != nil {
		t.Fatal(err)
	}
	got := s2.Get()
	if got.DeviceID != "kiosk_abc123" || got.Views[0].Type != ViewWeb || got.DefaultView != "ha" {
		t.Errorf("reloaded config wrong: %+v", got)
	}
}

func TestUpdateRejectsInvalid(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "c.json"), "k")
	if err != nil {
		t.Fatal(err)
	}
	bad := []func(*Config){
		func(c *Config) { c.Views = []View{{Name: "a", URL: "x"}, {Name: "a", URL: "y"}} },
		func(c *Config) { c.DefaultView = "missing" },
		func(c *Config) { c.Display.Rotation = 45 },
		func(c *Config) { c.MQTT.Enabled = true; c.MQTT.Host = "" },
		func(c *Config) { c.MQTT.BaseTopic = "a/#" },
		func(c *Config) { c.DeviceID = "Has Spaces" },
		func(c *Config) { c.Sleep.ScheduleEnabled = true; c.Sleep.ScheduleOff = "25:00" },
	}
	for i, fn := range bad {
		if err := s.Update(func(c *Config) error { fn(c); return nil }); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	if len(s.Get().Views) != 0 {
		t.Error("rejected update was stored")
	}
}

func TestUpdateWindow(t *testing.T) {
	at := func(day time.Weekday, hm string) time.Time {
		// 2026-11-15 is a Sunday.
		base := time.Date(2026, 11, 15, 0, 0, 0, 0, time.Local).AddDate(0, 0, int(day))
		p, _ := time.Parse("15:04", hm)
		return base.Add(time.Duration(p.Hour())*time.Hour + time.Duration(p.Minute())*time.Minute)
	}
	u := UpdateCheck{AutoFrom: "03:00", AutoTo: "05:00", AutoDays: []int{2}}
	if !u.InWindow(at(time.Tuesday, "03:30")) || u.InWindow(at(time.Tuesday, "05:00")) || u.InWindow(at(time.Monday, "03:30")) {
		t.Error("plain window wrong")
	}
	n := UpdateCheck{AutoFrom: "23:00", AutoTo: "02:00", AutoDays: []int{6}}
	if !n.InWindow(at(time.Saturday, "23:30")) || !n.InWindow(at(time.Sunday, "01:00")) || n.InWindow(at(time.Sunday, "23:30")) {
		t.Error("overnight window wrong")
	}
	all := UpdateCheck{AutoFrom: "03:00", AutoTo: "05:00"}
	if !all.InWindow(at(time.Friday, "04:59")) {
		t.Error("every-day window wrong")
	}
	bad := UpdateCheck{AutoFrom: "3am", AutoTo: "05:00"}
	if bad.validate() == nil {
		t.Error("bad time accepted")
	}
}
