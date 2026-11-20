package config

import "testing"

func sample() Config {
	c := Default("hall")
	c.DeviceName = "Hall"
	c.PasswordHash, c.APIToken = "hash-hall", "token-hall"
	c.MQTT.Password = "mqtt-secret"
	c.OIDC.ClientSecret = "oidc-secret"
	c.Views = []View{{Name: "home", Type: ViewWeb, URL: "https://ha.lan/", Auth: Auth{Type: AuthBearer, Token: "view-secret"}, Headers: []Header{{Name: "X-Key", Value: "hdr-secret"}}}}
	c.DefaultView = "home"
	c.Rotations = []Rotation{{Name: "day", Items: []RotationItem{{View: "home", Seconds: 30}}}}
	return c
}

func TestRedactHidesEverySecret(t *testing.T) {
	r := sample().Redact()
	if r.PasswordHash != "" || r.APIToken != "" || r.MQTT.Password != Redacted || r.OIDC.ClientSecret != Redacted ||
		r.Views[0].Auth.Token != Redacted || r.Views[0].Headers[0].Value != Redacted {
		t.Fatalf("secrets left: %+v", r)
	}
	if sample().Views[0].Auth.Token != "view-secret" {
		t.Fatal("Redact changed the original")
	}
}

func TestRestoreRedactedKeepsCurrentSecrets(t *testing.T) {
	cur := sample()
	src := sample()
	src.Views[0].URL = "https://ha.lan/new"
	src = src.Redact()
	if err := cur.Restore(src, Sections); err != nil {
		t.Fatal(err)
	}
	if cur.Views[0].URL != "https://ha.lan/new" {
		t.Error("view not restored")
	}
	if cur.Views[0].Auth.Token != "view-secret" || cur.Views[0].Headers[0].Value != "hdr-secret" ||
		cur.MQTT.Password != "mqtt-secret" || cur.OIDC.ClientSecret != "oidc-secret" {
		t.Errorf("redacted backup wiped secrets: %+v", cur)
	}
	if cur.PasswordHash != "hash-hall" || cur.APIToken != "token-hall" {
		t.Error("backup without access data changed the password or token")
	}
	if err := cur.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreOnlyChosenSections(t *testing.T) {
	cur := Default("office")
	cur.DeviceName = "Office"
	src := sample()
	src.Display.Rotation = 90
	if err := cur.Restore(src, []string{"views", "display"}); err != nil {
		t.Fatal(err)
	}
	if len(cur.Views) != 1 || cur.Display.Rotation != 90 {
		t.Error("chosen sections not restored")
	}
	if cur.DeviceID != "office" || cur.DeviceName != "Office" || cur.MQTT.Password != "" || cur.PasswordHash != "" {
		t.Errorf("unchosen sections changed: %+v", cur)
	}
	if err := cur.Restore(src, []string{"bogus"}); err == nil {
		t.Error("unknown section accepted")
	}
}

func TestRestoreDeviceMovesDefaultTopic(t *testing.T) {
	cur := Default("office")
	src := sample()
	if err := cur.Restore(src, []string{"device", "access"}); err != nil {
		t.Fatal(err)
	}
	if cur.DeviceID != "hall" || cur.MQTT.BaseTopic != "pikiosk/hall" || cur.PasswordHash != "hash-hall" {
		t.Errorf("device/access restore: %+v", cur)
	}
}
