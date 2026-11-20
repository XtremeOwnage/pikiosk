package config

import (
	"fmt"
	"slices"
	"strings"
)

// Redacted stands for a stored secret that is not shown or exported. On
// import or save it means "keep the current value".
const Redacted = "********"

// Sections are the parts of the configuration a backup can restore.
var Sections = []string{"views", "sounds", "mqtt", "display", "cast", "sso", "updates", "device", "access"}

// RedactView hides a view's credentials and header values.
func RedactView(v *View) {
	if v.Auth.Password != "" {
		v.Auth.Password = Redacted
	}
	if v.Auth.Token != "" {
		v.Auth.Token = Redacted
	}
	for i := range v.Headers {
		if v.Headers[i].Value != "" {
			v.Headers[i].Value = Redacted
		}
	}
}

// UnredactView puts back the secrets of old wherever v holds Redacted.
func UnredactView(v *View, old View) {
	if v.Auth.Password == Redacted {
		v.Auth.Password = old.Auth.Password
	}
	if v.Auth.Token == Redacted {
		v.Auth.Token = old.Auth.Token
	}
	if v.HAToken == Redacted {
		v.HAToken = old.Auth.Token
	}
	for i, h := range v.Headers {
		if h.Value != Redacted {
			continue
		}
		v.Headers[i].Value = ""
		for _, o := range old.Headers {
			if strings.EqualFold(o.Name, h.Name) {
				v.Headers[i].Value = o.Value
			}
		}
	}
}

// Redact hides every secret, for an export without passwords and tokens.
func (c Config) Redact() Config {
	c = clone(c)
	c.PasswordHash, c.APIToken = "", ""
	if c.MQTT.Password != "" {
		c.MQTT.Password = Redacted
	}
	if c.OIDC.ClientSecret != "" {
		c.OIDC.ClientSecret = Redacted
	}
	for i := range c.Views {
		RedactView(&c.Views[i])
	}
	return c
}

// Restore copies the given sections of src into c. Redacted secrets in src
// keep the values c already has, and an export without access data leaves
// the admin password and API token alone.
func (c *Config) Restore(src Config, sections []string) error {
	for _, s := range sections {
		if !slices.Contains(Sections, s) {
			return fmt.Errorf("unknown section %q", s)
		}
	}
	src = clone(src)
	has := func(s string) bool { return slices.Contains(sections, s) }
	if has("views") {
		for i := range src.Views {
			old, _ := c.View(src.Views[i].Name)
			UnredactView(&src.Views[i], old)
		}
		c.Views, c.DefaultView = src.Views, src.DefaultView
		c.Rotations, c.DefaultRotation = src.Rotations, src.DefaultRotation
	}
	if has("sounds") {
		c.Audio = src.Audio
	}
	if has("mqtt") {
		if src.MQTT.Password == Redacted {
			src.MQTT.Password = c.MQTT.Password
		}
		c.MQTT = src.MQTT
	}
	if has("display") {
		c.Display, c.Sleep = src.Display, src.Sleep
	}
	if has("cast") {
		c.Cast = src.Cast
	}
	if has("sso") {
		if src.OIDC.ClientSecret == Redacted {
			src.OIDC.ClientSecret = c.OIDC.ClientSecret
		}
		c.OIDC = src.OIDC
	}
	if has("updates") {
		c.UpdateCheck = src.UpdateCheck
	}
	if has("device") {
		// The base topic follows the ID when it was the default.
		if !has("mqtt") && c.MQTT.BaseTopic == "pikiosk/"+c.DeviceID {
			c.MQTT.BaseTopic = "pikiosk/" + src.DeviceID
		}
		c.DeviceName, c.DeviceID = src.DeviceName, src.DeviceID
	}
	if has("access") {
		if src.PasswordHash != "" {
			c.PasswordHash = src.PasswordHash
		}
		if src.APIToken != "" {
			c.APIToken = src.APIToken
		}
	}
	return nil
}
