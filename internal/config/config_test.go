package config

import (
	"os"
	"path/filepath"
	"testing"
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
