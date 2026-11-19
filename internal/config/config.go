// Package config holds the runtime configuration that the web UI edits.
// It lives in the state directory, outside the immutable system image.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
)

type ViewType string

const (
	ViewWeb    ViewType = "web"
	ViewStream ViewType = "stream"
)

type View struct {
	Name string   `json:"name"`
	Type ViewType `json:"type"`
	URL  string   `json:"url"`
	// Disabled views are skipped by rotations; they can still be shown
	// directly.
	Disabled bool `json:"disabled,omitempty"`
	Auth     Auth `json:"auth,omitzero"`
	// Headers are added to requests to the view's own origin.
	Headers []Header `json:"headers,omitempty"`
	// CSS is injected into every page of the view.
	CSS string `json:"css,omitempty"`
	// Zoom is the page zoom factor; 0 means 1.0.
	Zoom float64 `json:"zoom,omitempty"`
	// RefreshSeconds reloads a web view periodically; 0 disables.
	RefreshSeconds int `json:"refresh_seconds,omitempty"`
	// KeepLoaded leaves a web view running in a background tab after another
	// view is shown, so switching back is instant.
	KeepLoaded bool `json:"keep_loaded,omitempty"`

	// HAToken is the pre-Auth form of AuthHomeAssistant; read and migrated.
	HAToken string `json:"ha_token,omitempty"`
}

type AuthType string

const (
	AuthNone AuthType = ""
	// AuthBasic sends HTTP basic credentials with every request.
	AuthBasic AuthType = "basic"
	// AuthBearer sends "Authorization: Bearer <token>".
	AuthBearer AuthType = "bearer"
	// AuthHomeAssistant stores a long-lived access token where the Home
	// Assistant frontend looks for it, so dashboards load without a login.
	AuthHomeAssistant AuthType = "home_assistant"
)

type Auth struct {
	Type     AuthType `json:"type,omitempty"`
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	Token    string   `json:"token,omitempty"`
}

type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (v View) Equal(o View) bool { return reflect.DeepEqual(v, o) }

// Enabled reports whether the view exists and is not disabled.
func (c Config) Enabled(name string) bool {
	v, ok := c.View(name)
	return ok && !v.Disabled
}

// Rotation cycles through views; each step shows a view for Seconds.
type Rotation struct {
	Name  string         `json:"name"`
	Items []RotationItem `json:"items"`
}

type RotationItem struct {
	View    string `json:"view"`
	Seconds int    `json:"seconds"`
	// Disabled skips this step without removing it.
	Disabled bool `json:"disabled,omitempty"`
}

// OIDC signs users in to the web UI through an OpenID Connect provider such
// as Authentik. The admin password keeps working alongside it.
type OIDC struct {
	Enabled bool `json:"enabled"`
	// Issuer is the provider's issuer URL; discovery is read from
	// <issuer>/.well-known/openid-configuration.
	Issuer   string `json:"issuer"`
	ClientID string `json:"client_id"`
	// ClientSecret is empty for a public client, which uses PKCE alone.
	ClientSecret string `json:"client_secret,omitempty"`
	// RedirectURL overrides http(s)://<host the browser used>/api/oidc/callback.
	RedirectURL string `json:"redirect_url,omitempty"`
	// AllowedGroups limits sign-in to members of any of these groups (from
	// the groups claim). Empty allows every user the provider lets through.
	AllowedGroups []string `json:"allowed_groups,omitempty"`
	// ButtonLabel is shown on the login page.
	ButtonLabel string `json:"button_label,omitempty"`
}

type Cast struct {
	// AirPlay runs an AirPlay receiver for screen mirroring, video and audio.
	AirPlay bool `json:"airplay"`
	// Name shown to AirPlay clients; empty uses the device name.
	Name string `json:"name"`
	// PIN asks a new client for a code shown on the screen.
	PIN bool `json:"pin"`
}

type Audio struct {
	// Volume 0-100 applied at startup; -1 leaves the system volume alone.
	Volume int `json:"volume"`
}

type MQTT struct {
	Enabled         bool   `json:"enabled"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	TLS             bool   `json:"tls"`
	TLSInsecure     bool   `json:"tls_insecure"`
	Username        string `json:"username"`
	Password        string `json:"password"`
	BaseTopic       string `json:"base_topic"`
	Discovery       bool   `json:"discovery"`
	DiscoveryPrefix string `json:"discovery_prefix"`
}

type Display struct {
	// Rotation in degrees: 0, 90, 180 or 270.
	Rotation int     `json:"rotation"`
	Scale    float64 `json:"scale"`
	// Brightness 0-100 set over DDC/CI at startup; -1 leaves the monitor alone.
	Brightness int `json:"brightness"`
	// HideCursor hides the pointer after this many seconds; 0 never hides.
	HideCursorSeconds int `json:"hide_cursor_seconds"`
}

type Sleep struct {
	// IdleMinutes turns the screen off after this long without input or
	// commands; 0 disables.
	IdleMinutes int `json:"idle_minutes"`
	// WakeOnInput turns the screen back on when the touchscreen, mouse or
	// keyboard is used.
	WakeOnInput bool `json:"wake_on_input"`
	// WakeOnCommand turns the screen on when a view, URL or stream is shown.
	WakeOnCommand    bool   `json:"wake_on_command"`
	ScheduleEnabled  bool   `json:"schedule_enabled"`
	ScheduleOff      string `json:"schedule_off"` // "22:00"
	ScheduleOn       string `json:"schedule_on"`  // "06:30"
	ScheduleWeekdays []int  `json:"schedule_weekdays,omitempty"`
}

type Config struct {
	DeviceName  string     `json:"device_name"`
	DeviceID    string     `json:"device_id"`
	Views       []View     `json:"views"`
	DefaultView string     `json:"default_view"`
	Rotations   []Rotation `json:"rotations"`
	// DefaultRotation starts at boot instead of the default view.
	DefaultRotation string  `json:"default_rotation"`
	Audio           Audio   `json:"audio"`
	Cast            Cast    `json:"cast"`
	MQTT            MQTT    `json:"mqtt"`
	Display         Display `json:"display"`
	Sleep           Sleep   `json:"sleep"`
	OIDC            OIDC    `json:"oidc"`

	PasswordHash string `json:"password_hash,omitempty"`
	APIToken     string `json:"api_token,omitempty"`
}

var (
	idPattern     = regexp.MustCompile(`^[a-z0-9_-]{1,48}$`)
	headerPattern = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
)

func Default(deviceID string) Config {
	return Config{
		DeviceName: "Kiosk",
		DeviceID:   deviceID,
		Views:      []View{},
		Rotations:  []Rotation{},
		Audio:      Audio{Volume: -1},
		Cast:       Cast{AirPlay: true},
		MQTT: MQTT{
			Port:            1883,
			BaseTopic:       "pikiosk/" + deviceID,
			Discovery:       true,
			DiscoveryPrefix: "homeassistant",
		},
		Display: Display{Scale: 1, Brightness: -1, HideCursorSeconds: 3},
		Sleep:   Sleep{WakeOnInput: true, WakeOnCommand: true, ScheduleOff: "22:00", ScheduleOn: "07:00"},
	}
}

func (c *Config) Validate() error {
	if strings.TrimSpace(c.DeviceName) == "" {
		return errors.New("device name is required")
	}
	if !idPattern.MatchString(c.DeviceID) {
		return errors.New("device id must be 1-48 chars of a-z, 0-9, _ or -")
	}
	seen := map[string]bool{}
	for i := range c.Views {
		v := &c.Views[i]
		v.Name = strings.TrimSpace(v.Name)
		if v.Name == "" {
			return fmt.Errorf("view %d has no name", i+1)
		}
		if seen[v.Name] {
			return fmt.Errorf("duplicate view name %q", v.Name)
		}
		seen[v.Name] = true
		if v.Type == "" {
			v.Type = ViewWeb
		}
		if v.Type != ViewWeb && v.Type != ViewStream {
			return fmt.Errorf("view %q: type must be web or stream", v.Name)
		}
		if v.URL == "" {
			return fmt.Errorf("view %q has no URL", v.Name)
		}
		if v.Zoom < 0 || v.Zoom > 5 {
			return fmt.Errorf("view %q: zoom must be between 0.1 and 5", v.Name)
		}
		if v.RefreshSeconds < 0 {
			return fmt.Errorf("view %q: refresh must not be negative", v.Name)
		}
		if v.HAToken != "" {
			if v.Auth.Type == AuthNone {
				v.Auth = Auth{Type: AuthHomeAssistant, Token: v.HAToken}
			}
			v.HAToken = ""
		}
		switch v.Auth.Type {
		case AuthNone:
			v.Auth = Auth{}
		case AuthBasic:
			if v.Auth.Username == "" {
				return fmt.Errorf("view %q: basic auth needs a username", v.Name)
			}
			if strings.Contains(v.Auth.Username, ":") {
				return fmt.Errorf("view %q: username must not contain ':'", v.Name)
			}
			v.Auth.Token = ""
		case AuthBearer, AuthHomeAssistant:
			if v.Auth.Token == "" {
				return fmt.Errorf("view %q: auth needs a token", v.Name)
			}
			v.Auth.Username, v.Auth.Password = "", ""
		default:
			return fmt.Errorf("view %q: unknown auth type %q", v.Name, v.Auth.Type)
		}
		if v.Type == ViewStream && (v.Auth.Type != AuthNone || len(v.Headers) > 0 || v.CSS != "" || v.KeepLoaded) {
			return fmt.Errorf("view %q: auth, headers, CSS and keep loaded apply to web views only", v.Name)
		}
		hdrs := v.Headers[:0]
		for _, h := range v.Headers {
			h.Name = strings.TrimSpace(h.Name)
			if h.Name == "" && h.Value == "" {
				continue
			}
			if !headerPattern.MatchString(h.Name) {
				return fmt.Errorf("view %q: invalid header name %q", v.Name, h.Name)
			}
			if strings.ContainsAny(h.Value, "\r\n") {
				return fmt.Errorf("view %q: header %s has a line break", v.Name, h.Name)
			}
			hdrs = append(hdrs, h)
		}
		v.Headers = hdrs
		if len(v.Headers) == 0 {
			v.Headers = nil
		}
	}
	if c.DefaultView != "" && !seen[c.DefaultView] {
		return fmt.Errorf("default view %q does not exist", c.DefaultView)
	}
	rotSeen := map[string]bool{}
	for i := range c.Rotations {
		r := &c.Rotations[i]
		r.Name = strings.TrimSpace(r.Name)
		if r.Name == "" {
			return fmt.Errorf("rotation %d has no name", i+1)
		}
		if rotSeen[r.Name] {
			return fmt.Errorf("duplicate rotation name %q", r.Name)
		}
		rotSeen[r.Name] = true
		if len(r.Items) == 0 {
			return fmt.Errorf("rotation %q has no views", r.Name)
		}
		for _, it := range r.Items {
			if !seen[it.View] {
				return fmt.Errorf("rotation %q uses unknown view %q", r.Name, it.View)
			}
			if it.Seconds < 3 {
				return fmt.Errorf("rotation %q: each view needs at least 3 seconds", r.Name)
			}
		}
	}
	if c.DefaultRotation != "" && !rotSeen[c.DefaultRotation] {
		return fmt.Errorf("default rotation %q does not exist", c.DefaultRotation)
	}
	if err := c.OIDC.validate(); err != nil {
		return err
	}
	c.Cast.Name = strings.TrimSpace(c.Cast.Name)
	if len(c.Cast.Name) > 64 {
		return errors.New("cast name must be at most 64 characters")
	}
	if c.Audio.Volume < -1 || c.Audio.Volume > 100 {
		return errors.New("volume must be -1 or 0-100")
	}
	switch c.Display.Rotation {
	case 0, 90, 180, 270:
	default:
		return errors.New("rotation must be 0, 90, 180 or 270")
	}
	if c.Display.Scale == 0 {
		c.Display.Scale = 1
	}
	if c.Display.Scale < 0.5 || c.Display.Scale > 4 {
		return errors.New("scale must be between 0.5 and 4")
	}
	if c.Display.Brightness < -1 || c.Display.Brightness > 100 {
		return errors.New("brightness must be -1 or 0-100")
	}
	if c.MQTT.Enabled {
		if c.MQTT.Host == "" {
			return errors.New("MQTT host is required")
		}
		if c.MQTT.Port <= 0 || c.MQTT.Port > 65535 {
			return errors.New("MQTT port is invalid")
		}
	}
	c.MQTT.BaseTopic = strings.Trim(c.MQTT.BaseTopic, "/")
	if c.MQTT.BaseTopic == "" {
		c.MQTT.BaseTopic = "pikiosk/" + c.DeviceID
	}
	if strings.ContainsAny(c.MQTT.BaseTopic, "#+") {
		return errors.New("base topic must not contain wildcards")
	}
	if c.MQTT.DiscoveryPrefix == "" {
		c.MQTT.DiscoveryPrefix = "homeassistant"
	}
	if c.Sleep.IdleMinutes < 0 {
		return errors.New("idle minutes must not be negative")
	}
	if c.Sleep.ScheduleEnabled {
		if _, err := ParseClock(c.Sleep.ScheduleOff); err != nil {
			return fmt.Errorf("schedule off: %w", err)
		}
		if _, err := ParseClock(c.Sleep.ScheduleOn); err != nil {
			return fmt.Errorf("schedule on: %w", err)
		}
	}
	for _, d := range c.Sleep.ScheduleWeekdays {
		if d < 0 || d > 6 {
			return errors.New("weekdays are 0 (Sunday) to 6")
		}
	}
	return nil
}

func (c Config) View(name string) (View, bool) {
	for _, v := range c.Views {
		if v.Name == name {
			return v, true
		}
	}
	return View{}, false
}

func (c Config) Rotation(name string) (Rotation, bool) {
	for _, r := range c.Rotations {
		if r.Name == name {
			return r, true
		}
	}
	return Rotation{}, false
}

func (c Config) RotationNames() []string {
	names := make([]string, len(c.Rotations))
	for i, r := range c.Rotations {
		names[i] = r.Name
	}
	return names
}

func (c Config) ViewNames() []string {
	names := make([]string, len(c.Views))
	for i, v := range c.Views {
		names[i] = v.Name
	}
	return names
}

// ParseClock parses "HH:MM" into minutes after midnight.
func ParseClock(s string) (int, error) {
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("invalid time %q, want HH:MM", s)
	}
	return h*60 + m, nil
}

// Store persists Config as JSON and notifies subscribers on change.
type Store struct {
	path string
	mu   sync.RWMutex
	cfg  Config
	subs []func(old, new Config)
}

func Open(path, deviceID string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.cfg = Default(deviceID)
		s.cfg.APIToken = RandomToken()
		if err := s.write(s.cfg); err != nil {
			return nil, err
		}
		return s, nil
	case err != nil:
		return nil, err
	}
	cfg := Default(deviceID)
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.APIToken == "" {
		cfg.APIToken = RandomToken()
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s.cfg = cfg
	return s, nil
}

func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clone(s.cfg)
}

// Update applies fn to a copy, validates and saves it, then notifies
// subscribers. The stored config is unchanged if fn or validation fails.
func (s *Store) Update(fn func(*Config) error) error {
	s.mu.Lock()
	old := clone(s.cfg)
	next := clone(s.cfg)
	if err := fn(&next); err != nil {
		s.mu.Unlock()
		return err
	}
	if err := next.Validate(); err != nil {
		s.mu.Unlock()
		return err
	}
	if err := s.write(next); err != nil {
		s.mu.Unlock()
		return err
	}
	s.cfg = next
	subs := append([]func(old, new Config){}, s.subs...)
	s.mu.Unlock()
	for _, fn := range subs {
		fn(old, clone(next))
	}
	return nil
}

func (s *Store) Subscribe(fn func(old, new Config)) {
	s.mu.Lock()
	s.subs = append(s.subs, fn)
	s.mu.Unlock()
}

func (s *Store) write(cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func clone(c Config) Config {
	data, _ := json.Marshal(c)
	var out Config
	_ = json.Unmarshal(data, &out)
	return out
}

func RandomToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (o *OIDC) validate() error {
	o.Issuer = strings.TrimSpace(o.Issuer)
	o.ClientID = strings.TrimSpace(o.ClientID)
	o.RedirectURL = strings.TrimSpace(o.RedirectURL)
	o.ButtonLabel = strings.TrimSpace(o.ButtonLabel)
	groups := o.AllowedGroups[:0]
	for _, g := range o.AllowedGroups {
		if g = strings.TrimSpace(g); g != "" {
			groups = append(groups, g)
		}
	}
	o.AllowedGroups = groups
	if len(o.AllowedGroups) == 0 {
		o.AllowedGroups = nil
	}
	if !o.Enabled {
		return nil
	}
	if u, err := url.Parse(o.Issuer); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("single sign-on: issuer must be an http(s) URL")
	}
	if o.ClientID == "" {
		return errors.New("single sign-on: client ID is required")
	}
	if o.RedirectURL != "" {
		if u, err := url.Parse(o.RedirectURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return errors.New("single sign-on: redirect URL must be an http(s) URL")
		}
	}
	return nil
}
