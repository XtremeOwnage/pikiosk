package mqtt

import (
	"encoding/json"
	"testing"

	"github.com/XtremeOwnage/pikiosk/internal/config"
)

func TestSimpleCommand(t *testing.T) {
	if c := simpleCommand("screen", "OFF"); c.On == nil || *c.On {
		t.Error("OFF not parsed")
	}
	if c := simpleCommand("screen", "on"); c.On == nil || !*c.On {
		t.Error("on not parsed")
	}
	if c := simpleCommand("brightness", "42.0"); c.Value == nil || *c.Value != 42 {
		t.Error("brightness not parsed")
	}
	if c := simpleCommand("view", "cams"); c.Action != "view" || c.Name != "cams" {
		t.Errorf("view: %+v", c)
	}
}

func TestDiscovery(t *testing.T) {
	cfg := config.Default("kiosk_abc")
	cfg.Views = []config.View{{Name: "ha", Type: config.ViewWeb, URL: "https://ha.lan"}}
	d := Discovery(cfg, "1.0", "http://10.0.0.5")
	data, _ := json.Marshal(d)

	var out struct {
		Device struct {
			Identifiers []string `json:"identifiers"`
			ConfigURL   string   `json:"configuration_url"`
		} `json:"device"`
		Availability string                    `json:"availability_topic"`
		Components   map[string]map[string]any `json:"components"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Device.Identifiers[0] != "kiosk_abc" || out.Device.ConfigURL != "http://10.0.0.5" {
		t.Errorf("device: %+v", out.Device)
	}
	if out.Availability != "pikiosk/kiosk_abc/availability" {
		t.Errorf("availability: %s", out.Availability)
	}
	sel := out.Components["view"]
	if sel["p"] != "select" || sel["command_topic"] != "pikiosk/kiosk_abc/cmd/view" {
		t.Errorf("view component: %v", sel)
	}
	ids := map[string]bool{}
	for k, c := range out.Components {
		id, _ := c["unique_id"].(string)
		if id == "" || ids[id] {
			t.Errorf("component %s has missing or duplicate unique_id", k)
		}
		ids[id] = true
	}

	// No views: no select, since Home Assistant rejects an empty option list.
	cfg.Views = nil
	if _, ok := Discovery(cfg, "1.0", "")["components"].(map[string]any)["view"]; ok {
		t.Error("select published without options")
	}
}
