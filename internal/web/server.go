// Package web serves the configuration UI and the REST API.
package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/XtremeOwnage/pikiosk/internal/audio"
	"github.com/XtremeOwnage/pikiosk/internal/browser"
	"github.com/XtremeOwnage/pikiosk/internal/cast"
	"github.com/XtremeOwnage/pikiosk/internal/config"
	"github.com/XtremeOwnage/pikiosk/internal/kiosk"
	"github.com/XtremeOwnage/pikiosk/internal/mqtt"
	"github.com/XtremeOwnage/pikiosk/internal/player"
	"github.com/XtremeOwnage/pikiosk/internal/system"
)

//go:embed static
var static embed.FS

const (
	sessionCookie = "pikiosk_session"
	sessionTTL    = 7 * 24 * time.Hour
	redacted      = "********"
)

type Server struct {
	Config  *config.Store
	Kiosk   *kiosk.Kiosk
	Browser *browser.Browser
	Player  *player.Player
	Audio   *audio.Player
	Cast    *cast.Receiver
	MQTT    *mqtt.Bridge
	Stats   func() map[string]any
	Version string
	Port    string
	Log     *slog.Logger

	mu       sync.Mutex
	sessions map[string]time.Time
	failures map[string][]time.Time
}

func (s *Server) Handler() http.Handler {
	s.sessions = map[string]time.Time{}
	s.failures = map[string][]time.Time{}

	mux := http.NewServeMux()
	sub, _ := fs.Sub(static, "static")
	mux.Handle("GET /", http.FileServerFS(sub))
	mux.HandleFunc("GET /api.html", serveAPIDocs)

	// Open endpoints.
	mux.HandleFunc("GET /api/auth", s.authStatus)
	mux.HandleFunc("POST /api/setup", s.setup)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("POST /api/activity", s.activity)
	mux.HandleFunc("GET /api/welcome", s.welcome)

	api := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.requireAuth(h)) }
	api("GET /api/state", s.getState)
	api("POST /api/command", s.command)
	api("POST /api/view", s.cmdShortcut("view"))
	api("POST /api/url", s.cmdShortcut("url"))
	api("POST /api/stream", s.cmdShortcut("stream"))
	api("POST /api/stream/stop", s.cmdShortcut("stop_stream"))
	api("POST /api/reload", s.cmdShortcut("reload"))
	api("POST /api/screen", s.cmdShortcut("screen"))
	api("POST /api/brightness", s.cmdShortcut("brightness"))
	api("POST /api/view/enable", s.cmdShortcut("enable_view"))
	api("POST /api/view/disable", s.cmdShortcut("disable_view"))
	api("POST /api/rotation", s.cmdShortcut("rotation"))
	api("POST /api/rotation/resume", s.cmdShortcut("resume_rotation"))
	api("POST /api/rotation/stop", s.cmdShortcut("stop_rotation"))
	api("POST /api/rotation/next", s.cmdShortcut("next"))
	api("POST /api/sound/play", s.cmdShortcut("play_sound"))
	api("POST /api/sound/stop", s.cmdShortcut("stop_sound"))
	api("POST /api/volume", s.cmdShortcut("volume"))
	api("GET /api/sounds", s.listSounds)
	api("POST /api/sounds", s.uploadSound)
	api("DELETE /api/sounds/{name}", s.deleteSound)
	api("GET /api/events", s.getEvents)
	api("GET /api/screenshot", s.screenshot)
	api("POST /api/input", s.input)
	api("POST /api/browser/clear", s.clearBrowser)
	api("POST /api/browser/restart", s.cmdShortcut("restart_browser"))

	api("GET /api/config", s.getConfig)
	api("PUT /api/config/general", s.putGeneral)
	api("PUT /api/config/views", s.putViews)
	api("PUT /api/config/rotations", s.putRotations)
	api("PUT /api/config/audio", s.putAudio)
	api("PUT /api/config/cast", s.putCast)
	api("PUT /api/config/mqtt", s.putMQTT)
	api("PUT /api/config/display", s.putDisplay)
	api("PUT /api/config/sleep", s.putSleep)
	api("POST /api/password", s.changePassword)
	api("POST /api/token", s.regenerateToken)

	api("GET /api/network", s.getNetwork)
	api("POST /api/network/wifi/scan", s.scanWiFi)
	api("POST /api/network/wifi/connect", s.connectWiFi)
	api("POST /api/network/ipv4", s.setIPv4)

	api("GET /api/system", s.getSystem)
	api("POST /api/system/reboot", s.reboot)
	api("POST /api/system/update", s.startUnit(system.UpgradeUnit))
	api("POST /api/system/rollback", s.startUnit(system.RollbackUnit))
	api("GET /api/system/update", s.unitStatus(system.UpgradeUnit))
	api("POST /api/system/timezone", s.setTimezone)

	return securityHeaders(mux)
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' blob: data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
		h.ServeHTTP(w, r)
	})
}

// --- auth ---

func (s *Server) authorized(r *http.Request) bool {
	cfg := s.Config.Get()
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") && cfg.APIToken != "" {
		return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(h, "Bearer ")), []byte(cfg.APIToken)) == 1
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sessions[c.Value]
	if !ok || time.Now().After(exp) {
		delete(s.sessions, c.Value)
		return false
	}
	return true
}

func (s *Server) requireAuth(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeErr(w, http.StatusUnauthorized, errors.New("login required"))
			return
		}
		// Cookie sessions only accept JSON bodies, which a cross-site form
		// cannot send without a CORS preflight.
		if r.Method != http.MethodGet && r.Header.Get("Authorization") == "" &&
			r.ContentLength != 0 && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") &&
			r.Header.Get("X-Pikiosk") == "" {
			writeErr(w, http.StatusUnsupportedMediaType, errors.New("JSON body required"))
			return
		}
		h(w, r)
	})
}

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config.Get()
	writeJSON(w, map[string]any{
		"setup_required": cfg.PasswordHash == "",
		"authenticated":  s.authorized(r),
		"device_name":    cfg.DeviceName,
	})
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Password) < 8 {
		writeErr(w, http.StatusBadRequest, errors.New("password must be at least 8 characters"))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	err = s.Config.Update(func(c *config.Config) error {
		if c.PasswordHash != "" {
			return errors.New("already set up")
		}
		c.PasswordHash = string(hash)
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	s.newSession(w)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.throttled(ip) {
		writeErr(w, http.StatusTooManyRequests, errors.New("too many attempts, wait a minute"))
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	hash := s.Config.Get().PasswordHash
	if hash == "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil {
		s.recordFailure(ip)
		writeErr(w, http.StatusUnauthorized, errors.New("wrong password"))
		return
	}
	s.newSession(w)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) newSession(w http.ResponseWriter) {
	tok := config.RandomToken()
	s.mu.Lock()
	now := time.Now()
	for k, exp := range s.sessions {
		if now.After(exp) {
			delete(s.sessions, k)
		}
	}
	s.sessions[tok] = now.Add(sessionTTL)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: int(sessionTTL.Seconds()),
	})
}

func (s *Server) throttled(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-time.Minute)
	var recent []time.Time
	for _, t := range s.failures[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	s.failures[ip] = recent
	return len(recent) >= 5
}

func (s *Server) recordFailure(ip string) {
	s.mu.Lock()
	s.failures[ip] = append(s.failures[ip], time.Now())
	s.mu.Unlock()
}

// activity is called by swayidle on local input; loopback only.
func (s *Server) activity(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		writeErr(w, http.StatusForbidden, errors.New("loopback only"))
		return
	}
	s.Kiosk.Activity(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// welcome feeds the on-screen setup page; loopback only because it is shown
// before a password exists.
func (s *Server) welcome(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		writeErr(w, http.StatusForbidden, errors.New("loopback only"))
		return
	}
	cfg := s.Config.Get()
	info := system.GetInfo(s.Version)
	writeJSON(w, map[string]any{
		"device_name":    cfg.DeviceName,
		"hostname":       info.Hostname,
		"addresses":      info.Addresses,
		"port":           s.Port,
		"setup_required": cfg.PasswordHash == "",
		"version":        s.Version,
	})
}

// --- control ---

func (s *Server) getState(w http.ResponseWriter, r *http.Request) {
	url, playing := s.Player.Playing()
	writeJSON(w, map[string]any{
		"state":   s.Kiosk.State(),
		"browser": s.Browser.Status(),
		"player":  map[string]any{"playing": playing, "url": player.Redact(url), "last_error": s.Player.LastError()},
		"mqtt":    s.MQTT.Status(),
		"cast":    s.Cast.Status(),
		"device":  s.Stats(),
	})
}

func (s *Server) command(w http.ResponseWriter, r *http.Request) {
	var cmd mqtt.Command
	if !decode(w, r, &cmd) {
		return
	}
	s.run(w, r, cmd)
}

func (s *Server) cmdShortcut(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var cmd mqtt.Command
		if r.ContentLength != 0 && !decode(w, r, &cmd) {
			return
		}
		cmd.Action = action
		s.run(w, r, cmd)
	}
}

func (s *Server) run(w http.ResponseWriter, r *http.Request, cmd mqtt.Command) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := mqtt.Execute(ctx, s.Kiosk, cmd); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, s.Kiosk.State())
}

func (s *Server) screenshot(w http.ResponseWriter, r *http.Request) {
	img, err := s.Browser.Screenshot(r.Context(), 60)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(img)
}

func (s *Server) input(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type string  `json:"type"` // click, scroll, text, key
		X    float64 `json:"x"`
		Y    float64 `json:"y"`
		DY   float64 `json:"dy"`
		Text string  `json:"text"`
		Key  string  `json:"key"`
	}
	if !decode(w, r, &body) {
		return
	}
	ctx := r.Context()
	var err error
	switch body.Type {
	case "click":
		err = s.Browser.Click(ctx, body.X, body.Y)
	case "scroll":
		err = s.Browser.Scroll(ctx, body.X, body.Y, body.DY)
	case "text":
		err = s.Browser.Type(ctx, body.Text)
	case "key":
		err = s.Browser.Key(ctx, body.Key)
	default:
		err = errors.New("type must be click, scroll, text or key")
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.Kiosk.Activity(ctx)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) clearBrowser(w http.ResponseWriter, r *http.Request) {
	if err := s.Browser.ClearData(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, err)
		return
	}
	_ = s.Browser.Reload(r.Context())
	writeJSON(w, map[string]bool{"ok": true})
}

// --- sounds & events ---

func (s *Server) listSounds(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Audio.List())
}

func (s *Server) uploadSound(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Pikiosk") == "" && r.Header.Get("Authorization") == "" {
		writeErr(w, http.StatusBadRequest, errors.New("missing X-Pikiosk header"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, audio.MaxUpload+1<<20)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	defer f.Close()
	snd, err := s.Audio.Save(hdr.Filename, f)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.Kiosk.Emit("sound_added", snd.Name)
	s.MQTT.PublishSounds()
	writeJSON(w, snd)
}

func (s *Server) deleteSound(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.Audio.Delete(name); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	s.Kiosk.Emit("sound_deleted", name)
	s.MQTT.PublishSounds()
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) getEvents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Kiosk.Events())
}

// --- config ---

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	c := s.Config.Get()
	c.PasswordHash = ""
	if c.MQTT.Password != "" {
		c.MQTT.Password = redacted
	}
	for i := range c.Views {
		redactView(&c.Views[i])
	}
	writeJSON(w, c)
}

// redactView hides credentials and header values.
func redactView(v *config.View) {
	if v.Auth.Password != "" {
		v.Auth.Password = redacted
	}
	if v.Auth.Token != "" {
		v.Auth.Token = redacted
	}
	for i := range v.Headers {
		if v.Headers[i].Value != "" {
			v.Headers[i].Value = redacted
		}
	}
}

func unredactView(v *config.View, old config.View) {
	if v.Auth.Password == redacted {
		v.Auth.Password = old.Auth.Password
	}
	if v.Auth.Token == redacted {
		v.Auth.Token = old.Auth.Token
	}
	if v.HAToken == redacted {
		v.HAToken = old.Auth.Token
	}
	for i, h := range v.Headers {
		if h.Value != redacted {
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

func (s *Server) update(w http.ResponseWriter, fn func(*config.Config) error) {
	if err := s.Config.Update(fn); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) putGeneral(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceName  string `json:"device_name"`
		DeviceID    string `json:"device_id"`
		DefaultView string `json:"default_view"`
	}
	if !decode(w, r, &body) {
		return
	}
	s.update(w, func(c *config.Config) error {
		if c.MQTT.BaseTopic == "pikiosk/"+c.DeviceID {
			c.MQTT.BaseTopic = "pikiosk/" + body.DeviceID
		}
		c.DeviceName, c.DeviceID, c.DefaultView = body.DeviceName, body.DeviceID, body.DefaultView
		return nil
	})
}

func (s *Server) putViews(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Views       []config.View `json:"views"`
		DefaultView string        `json:"default_view"`
	}
	if !decode(w, r, &body) {
		return
	}
	s.update(w, func(c *config.Config) error {
		// A redacted secret means "unchanged"; match it to the old view by name.
		for i := range body.Views {
			old, _ := c.View(body.Views[i].Name)
			unredactView(&body.Views[i], old)
		}
		c.Views = body.Views
		c.DefaultView = body.DefaultView
		return nil
	})
}

func (s *Server) putRotations(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Rotations       []config.Rotation `json:"rotations"`
		DefaultRotation string            `json:"default_rotation"`
	}
	if !decode(w, r, &body) {
		return
	}
	s.update(w, func(c *config.Config) error {
		c.Rotations = body.Rotations
		c.DefaultRotation = body.DefaultRotation
		return nil
	})
}

func (s *Server) putAudio(w http.ResponseWriter, r *http.Request) {
	var a config.Audio
	if !decode(w, r, &a) {
		return
	}
	s.update(w, func(c *config.Config) error { c.Audio = a; return nil })
	if a.Volume >= 0 {
		_ = s.Kiosk.SetVolume(r.Context(), a.Volume)
	}
}

func (s *Server) putCast(w http.ResponseWriter, r *http.Request) {
	var c config.Cast
	if !decode(w, r, &c) {
		return
	}
	s.update(w, func(cfg *config.Config) error { cfg.Cast = c; return nil })
}

func (s *Server) putMQTT(w http.ResponseWriter, r *http.Request) {
	var m config.MQTT
	if !decode(w, r, &m) {
		return
	}
	s.update(w, func(c *config.Config) error {
		if m.Password == redacted {
			m.Password = c.MQTT.Password
		}
		c.MQTT = m
		return nil
	})
}

func (s *Server) putDisplay(w http.ResponseWriter, r *http.Request) {
	var d config.Display
	if !decode(w, r, &d) {
		return
	}
	s.update(w, func(c *config.Config) error { c.Display = d; return nil })
}

func (s *Server) putSleep(w http.ResponseWriter, r *http.Request) {
	var sl config.Sleep
	if !decode(w, r, &sl) {
		return
	}
	s.update(w, func(c *config.Config) error { c.Sleep = sl; return nil })
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.New) < 8 {
		writeErr(w, http.StatusBadRequest, errors.New("password must be at least 8 characters"))
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(s.Config.Get().PasswordHash), []byte(body.Current)) != nil {
		writeErr(w, http.StatusUnauthorized, errors.New("current password is wrong"))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.New), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.mu.Lock()
	s.sessions = map[string]time.Time{}
	s.mu.Unlock()
	if err := s.Config.Update(func(c *config.Config) error { c.PasswordHash = string(hash); return nil }); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.newSession(w)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) regenerateToken(w http.ResponseWriter, r *http.Request) {
	tok := config.RandomToken()
	if err := s.Config.Update(func(c *config.Config) error { c.APIToken = tok; return nil }); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]string{"api_token": tok})
}

// --- network & system ---

func (s *Server) getNetwork(w http.ResponseWriter, r *http.Request) {
	ifs, err := system.Interfaces(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, ifs)
}

func (s *Server) scanWiFi(w http.ResponseWriter, r *http.Request) {
	nets, err := system.ScanWiFi(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, nets)
}

func (s *Server) connectWiFi(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SSID     string `json:"ssid"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := system.ConnectWiFi(r.Context(), body.SSID, body.Password); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) setIPv4(w http.ResponseWriter, r *http.Request) {
	var body system.IPv4Settings
	if !decode(w, r, &body) {
		return
	}
	// Applying may drop this connection; finish the response first.
	if err := validateIPv4(body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := system.SetIPv4(ctx, body); err != nil {
			s.Log.Warn("set ipv4", "err", err)
		}
	}()
}

func validateIPv4(s system.IPv4Settings) error {
	if s.Connection == "" {
		return errors.New("connection is required")
	}
	if s.Method != "auto" && s.Method != "manual" {
		return errors.New("method must be auto or manual")
	}
	if s.Method == "manual" {
		if _, _, err := net.ParseCIDR(s.Address); err != nil {
			return errors.New("address must be CIDR like 192.168.1.50/24")
		}
	}
	return nil
}

func (s *Server) getSystem(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"info":      system.GetInfo(s.Version),
		"api_token": s.Config.Get().APIToken,
	})
}

func (s *Server) reboot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]bool{"ok": true})
	go func() {
		time.Sleep(time.Second)
		if err := system.Reboot(context.Background()); err != nil {
			s.Log.Warn("reboot", "err", err)
		}
	}()
}

func (s *Server) startUnit(unit string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := system.StartUnit(r.Context(), unit); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	}
}

func (s *Server) unitStatus(unit string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st, err := system.UnitInfo(r.Context(), unit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, st)
	}
}

func (s *Server) setTimezone(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Timezone string `json:"timezone"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := system.SetTimezone(r.Context(), body.Timezone); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// --- helpers ---

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isLoopback(r *http.Request) bool {
	ip := net.ParseIP(clientIP(r))
	return ip != nil && ip.IsLoopback()
}
