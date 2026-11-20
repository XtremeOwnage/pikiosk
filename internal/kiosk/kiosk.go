// Package kiosk owns what is on screen: the active view, temporary
// overrides that revert after a duration, screen power and sleep rules.
package kiosk

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/XtremeOwnage/pikiosk/internal/audio"
	"github.com/XtremeOwnage/pikiosk/internal/browser"
	"github.com/XtremeOwnage/pikiosk/internal/config"
	"github.com/XtremeOwnage/pikiosk/internal/display"
	"github.com/XtremeOwnage/pikiosk/internal/player"
	"github.com/XtremeOwnage/pikiosk/internal/system"
)

type Mode string

const (
	ModeWeb    Mode = "web"
	ModeStream Mode = "stream"
	ModeIdle   Mode = "idle"
)

type State struct {
	Mode         Mode      `json:"mode"`
	View         string    `json:"view"`
	URL          string    `json:"url"`
	Screen       bool      `json:"screen"`
	Brightness   int       `json:"brightness"`
	RevertAt     time.Time `json:"revert_at,omitzero"`
	LastActivity time.Time `json:"last_activity"`
	ScreenReason string    `json:"screen_reason,omitempty"`

	Rotation       string `json:"rotation"`
	RotationActive bool   `json:"rotation_active"`
	RotationIndex  int    `json:"rotation_index"`

	Sound  string `json:"sound"`
	Volume int    `json:"volume"`

	// Casting is true while a phone or computer is casting: AirPlay, or
	// YouTube's TV app launched from a phone.
	Casting bool `json:"casting"`
	// CastSource is "airplay" or "youtube" while casting.
	CastSource string `json:"cast_source,omitempty"`
}

// Event is one entry in the activity feed.
type Event struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`
	Message string    `json:"message"`
}

type rotation struct {
	name   string
	items  []config.RotationItem
	idx    int
	active bool
	timer  *time.Timer
	// preload loads the next step in the background before it is due.
	preload *time.Timer
	gen     int
}

// preloadLead is how long before a step ends the next one starts loading.
const preloadLead = 15 * time.Second

type target struct {
	view string
	url  string
	mode Mode
	page browser.Page
	// cast names the web cast this target belongs to ("youtube").
	cast string
}

type Kiosk struct {
	cfg     *config.Store
	browser *browser.Browser
	player  *player.Player
	audio   *audio.Player
	display *display.Display
	log     *slog.Logger
	welcome string

	mu        sync.Mutex
	state     State
	base      target // what to return to when an override expires
	revert    *time.Timer
	refresh   context.CancelFunc
	listeners []func(State)
	eventSubs []func(Event)
	events    []Event
	schedOff  *bool // last schedule state acted on
	rot       rotation
	airplay   bool
	webCast   string             // source of the running web cast
	castIdle  context.CancelFunc // stops the web cast idle watcher
}

// New creates the kiosk. welcomeURL is shown while no default view is set.
func New(cfg *config.Store, b *browser.Browser, p *player.Player, a *audio.Player, d *display.Display, welcomeURL string, log *slog.Logger) *Kiosk {
	c := cfg.Get()
	k := &Kiosk{
		cfg: cfg, browser: b, player: p, audio: a, display: d, welcome: welcomeURL,
		log: log.With("component", "kiosk"),
		state: State{
			Mode: ModeIdle, Screen: true, Brightness: c.Display.Brightness,
			Volume: c.Audio.Volume, LastActivity: time.Now(),
		},
	}
	p.OnEnd(func(u string, err error) { k.streamEnded(u, err) })
	a.OnEnd(func(src string, err error) {
		k.mu.Lock()
		k.state.Sound = ""
		k.mu.Unlock()
		if err != nil {
			k.Emit("sound_error", fmt.Sprintf("%s: %v", player.Redact(src), err))
		} else {
			k.Emit("sound_finished", player.Redact(src))
		}
		k.publish()
	})
	cfg.Subscribe(k.configChanged)
	return k
}

// Subscribe registers fn to receive every state change.
func (k *Kiosk) Subscribe(fn func(State)) {
	k.mu.Lock()
	k.listeners = append(k.listeners, fn)
	k.mu.Unlock()
}

func (k *Kiosk) State() State {
	k.mu.Lock()
	defer k.mu.Unlock()
	st := k.state
	st.Rotation, st.RotationActive, st.RotationIndex = k.rot.name, k.rot.active, k.rot.idx
	return st
}

// SubscribeEvents registers fn to receive activity events.
func (k *Kiosk) SubscribeEvents(fn func(Event)) {
	k.mu.Lock()
	k.eventSubs = append(k.eventSubs, fn)
	k.mu.Unlock()
}

// Events returns the most recent activity, newest last.
func (k *Kiosk) Events() []Event {
	k.mu.Lock()
	defer k.mu.Unlock()
	return slices.Clone(k.events)
}

// Emit records an activity event and passes it to subscribers.
func (k *Kiosk) Emit(typ, msg string) {
	ev := Event{Time: time.Now(), Type: typ, Message: msg}
	k.mu.Lock()
	k.events = append(k.events, ev)
	if len(k.events) > 100 {
		k.events = k.events[len(k.events)-100:]
	}
	subs := slices.Clone(k.eventSubs)
	k.mu.Unlock()
	k.log.Info("event", "type", typ, "message", msg)
	for _, fn := range subs {
		fn(ev)
	}
}

func (k *Kiosk) Run(ctx context.Context) {
	k.applyDisplay(ctx, k.cfg.Get())
	if v := k.cfg.Get().Audio.Volume; v >= 0 {
		if err := k.audio.SetVolume(ctx, v); err != nil {
			k.log.Debug("volume", "err", err)
		}
	}
	if r := k.cfg.Get().DefaultRotation; r != "" {
		if err := k.StartRotation(ctx, r); err != nil {
			k.log.Warn("default rotation", "err", err)
		}
	} else if v := k.cfg.Get().DefaultView; v != "" {
		if err := k.ShowView(ctx, v, 0); err != nil {
			k.log.Warn("default view", "err", err)
		}
	} else {
		k.showWelcome(ctx)
	}
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	// sway may start after kioskd; reapply display settings once it is up.
	displayApplied := false
	swayFailures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			running, err := k.display.Ping(ctx)
			switch {
			case !running:
				displayApplied = false
			case err != nil:
				swayFailures++
				k.log.Warn("compositor not responding", "failures", swayFailures, "err", err)
				if swayFailures >= 4 {
					k.Emit("watchdog", "display session hung; restarting it")
					_ = k.display.RestartCompositor()
					swayFailures = 0
					displayApplied = false
				}
			default:
				swayFailures = 0
				if !displayApplied {
					displayApplied = k.applyDisplay(ctx, k.cfg.Get()) == nil
					if displayApplied {
						k.reapplyScreen(ctx)
					}
				}
			}
			k.checkSleep(ctx, system.Now())
		}
	}
}

// ShowView switches to a configured view. A positive duration makes it
// temporary: the previous view comes back when it expires.
func (k *Kiosk) ShowView(ctx context.Context, name string, d time.Duration) error {
	t, refresh, err := k.viewTarget(name)
	if err != nil {
		return err
	}
	return k.show(ctx, t, d, refresh)
}

func (k *Kiosk) viewTarget(name string) (target, int, error) {
	v, ok := k.cfg.Get().View(name)
	if !ok {
		return target{}, 0, fmt.Errorf("unknown view %q", name)
	}
	t := target{view: v.Name, url: v.URL}
	if v.Type == config.ViewStream {
		t.mode = ModeStream
	} else {
		t.mode = ModeWeb
		t.page = pageFor(v)
	}
	return t, v.RefreshSeconds, nil
}

func pageFor(v config.View) browser.Page {
	p := browser.Page{URL: v.URL, CSS: v.CSS, Zoom: v.Zoom, Keep: v.KeepLoaded}
	if len(v.Headers) > 0 {
		p.Headers = map[string]string{}
		for _, h := range v.Headers {
			p.Headers[h.Name] = h.Value
		}
	}
	setAuth := func(val string) {
		if p.Headers == nil {
			p.Headers = map[string]string{}
		}
		p.Headers["Authorization"] = val
	}
	switch v.Auth.Type {
	case config.AuthBasic:
		setAuth("Basic " + base64.StdEncoding.EncodeToString([]byte(v.Auth.Username+":"+v.Auth.Password)))
	case config.AuthBearer:
		setAuth("Bearer " + v.Auth.Token)
	case config.AuthHomeAssistant:
		p.HAToken = v.Auth.Token
	}
	return p
}

// SetViewEnabled includes or skips a view in rotations.
func (k *Kiosk) SetViewEnabled(name string, on bool) error {
	err := k.cfg.Update(func(c *config.Config) error {
		for i := range c.Views {
			if c.Views[i].Name == name {
				c.Views[i].Disabled = !on
				return nil
			}
		}
		return fmt.Errorf("unknown view %q", name)
	})
	if err == nil {
		k.Emit("view_enabled", fmt.Sprintf("%s: %t", name, on))
	}
	return err
}

// StartRotation cycles through a rotation profile from its first view.
func (k *Kiosk) StartRotation(ctx context.Context, name string) error {
	r, ok := k.cfg.Get().Rotation(name)
	if !ok {
		return fmt.Errorf("unknown rotation %q", name)
	}
	k.mu.Lock()
	k.stopRevertLocked()
	k.stopRotationTimerLocked()
	k.rot.name, k.rot.items, k.rot.idx, k.rot.active = r.Name, r.Items, 0, true
	k.mu.Unlock()
	k.Emit("rotation_started", r.Name)
	return k.rotationShow(ctx)
}

// ResumeRotation continues the last rotation where it left off.
func (k *Kiosk) ResumeRotation(ctx context.Context) error {
	k.mu.Lock()
	name := k.rot.name
	k.mu.Unlock()
	if name == "" {
		if def := k.cfg.Get().DefaultRotation; def != "" {
			return k.StartRotation(ctx, def)
		}
		return errors.New("no rotation to resume")
	}
	r, ok := k.cfg.Get().Rotation(name)
	if !ok {
		return fmt.Errorf("rotation %q no longer exists", name)
	}
	k.mu.Lock()
	k.stopRevertLocked()
	k.rot.items = r.Items
	if k.rot.idx >= len(r.Items) {
		k.rot.idx = 0
	}
	k.rot.active = true
	k.mu.Unlock()
	k.Emit("rotation_resumed", name)
	return k.rotationShow(ctx)
}

// StopRotation stays on the current view and stops advancing.
func (k *Kiosk) StopRotation() {
	k.mu.Lock()
	was := k.rot.active
	k.rot.active = false
	k.stopRotationTimerLocked()
	k.mu.Unlock()
	if was {
		k.Emit("rotation_stopped", k.rot.name)
		k.publish()
	}
}

// NextInRotation skips to the next view of the active rotation.
func (k *Kiosk) NextInRotation(ctx context.Context) error {
	k.mu.Lock()
	if !k.rot.active || len(k.rot.items) == 0 {
		k.mu.Unlock()
		return errors.New("no rotation is running")
	}
	k.stopRotationTimerLocked()
	k.rot.idx = (k.rot.idx + 1) % len(k.rot.items)
	k.mu.Unlock()
	return k.rotationShow(ctx)
}

func (k *Kiosk) rotationShow(ctx context.Context) error {
	k.mu.Lock()
	if !k.rot.active || len(k.rot.items) == 0 {
		k.mu.Unlock()
		return nil
	}
	cfg := k.cfg.Get()
	n := len(k.rot.items)
	skipped := 0
	for skipped < n && (k.rot.items[k.rot.idx].Disabled || !cfg.Enabled(k.rot.items[k.rot.idx].View)) {
		k.rot.idx = (k.rot.idx + 1) % n
		skipped++
	}
	idle := skipped == n
	item := k.rot.items[k.rot.idx]
	k.rot.gen++
	gen := k.rot.gen
	k.mu.Unlock()

	var (
		t       target
		refresh int
		err     error
	)
	if idle {
		// Every view is disabled: keep the screen as it is and check again.
		err = errors.New("every view in the rotation is disabled")
	} else {
		t, refresh, err = k.viewTarget(item.View)
	}
	if err == nil {
		k.mu.Lock()
		k.base = t
		k.state.RevertAt = time.Time{}
		k.mu.Unlock()
		err = k.present(ctx, t, refresh)
	}
	k.mu.Lock()
	if k.rot.gen == gen && k.rot.active && !idle {
		if next, ok := k.nextStepLocked(k.cfg.Get()); ok {
			k.rot.preload = time.AfterFunc(preloadDelay(time.Duration(item.Seconds)*time.Second), func() {
				k.mu.Lock()
				live := k.rot.gen == gen && k.rot.active
				k.mu.Unlock()
				if !live {
					return
				}
				if err := k.browser.Preload(context.Background(), next); err != nil {
					k.log.Warn("preload", "url", next.URL, "err", err)
				}
			})
		}
	}
	if k.rot.gen == gen && k.rot.active {
		k.rot.timer = time.AfterFunc(time.Duration(item.Seconds)*time.Second, func() {
			k.mu.Lock()
			if k.rot.gen != gen || !k.rot.active {
				k.mu.Unlock()
				return
			}
			k.rot.idx = (k.rot.idx + 1) % len(k.rot.items)
			k.mu.Unlock()
			_ = k.rotationShow(context.Background())
		})
	}
	k.mu.Unlock()
	k.publish()
	return err
}

func (k *Kiosk) stopRotationTimerLocked() {
	k.rot.gen++
	if k.rot.timer != nil {
		k.rot.timer.Stop()
		k.rot.timer = nil
	}
	if k.rot.preload != nil {
		k.rot.preload.Stop()
		k.rot.preload = nil
	}
}

// nextStepLocked returns the web page of the next step that will be shown
// after the current one, if it is a web view other than the current one.
func (k *Kiosk) nextStepLocked(cfg config.Config) (browser.Page, bool) {
	n := len(k.rot.items)
	for i := 1; i < n; i++ {
		it := k.rot.items[(k.rot.idx+i)%n]
		if it.Disabled || !cfg.Enabled(it.View) {
			continue
		}
		if it.View == k.rot.items[k.rot.idx].View {
			return browser.Page{}, false
		}
		v, _ := cfg.View(it.View)
		if v.Type != config.ViewWeb {
			return browser.Page{}, false
		}
		return pageFor(v), true
	}
	return browser.Page{}, false
}

// preloadDelay leaves the current step time to load before the next one
// competes for the CPU.
func preloadDelay(step time.Duration) time.Duration {
	return max(step-preloadLead, step/3)
}

func (k *Kiosk) stopRevertLocked() {
	if k.revert != nil {
		k.revert.Stop()
		k.revert = nil
	}
	k.state.RevertAt = time.Time{}
}

// returnToBase ends a temporary override: the rotation continues if one is
// running, otherwise the base view comes back.
func (k *Kiosk) returnToBase(ctx context.Context) error {
	k.mu.Lock()
	active := k.rot.active
	k.stopRevertLocked()
	k.mu.Unlock()
	if active {
		return k.rotationShow(ctx)
	}
	return k.restoreBase(ctx)
}

// PlaySound plays a library sound, a library path or an http(s) URL.
func (k *Kiosk) PlaySound(ctx context.Context, ref string, volume int) error {
	src, err := k.audio.Play(ref, volume)
	if err != nil {
		return err
	}
	k.mu.Lock()
	k.state.Sound = player.Redact(src)
	k.mu.Unlock()
	k.Emit("sound_started", player.Redact(src))
	k.publish()
	return nil
}

func (k *Kiosk) StopSound() {
	if _, playing := k.audio.Playing(); !playing {
		return
	}
	k.audio.Stop()
	k.mu.Lock()
	k.state.Sound = ""
	k.mu.Unlock()
	k.Emit("sound_stopped", "")
	k.publish()
}

func (k *Kiosk) SetVolume(ctx context.Context, percent int) error {
	if err := k.audio.SetVolume(ctx, percent); err != nil {
		return err
	}
	k.mu.Lock()
	k.state.Volume = percent
	k.mu.Unlock()
	k.Emit("volume", fmt.Sprintf("%d%%", percent))
	k.publish()
	return nil
}

func (k *Kiosk) ShowURL(ctx context.Context, raw string, d time.Duration) error {
	if err := checkURL(raw, "http", "https"); err != nil {
		return err
	}
	return k.show(ctx, target{mode: ModeWeb, url: raw, page: browser.Page{URL: raw}}, d, 0)
}

func (k *Kiosk) PlayStream(ctx context.Context, raw string, d time.Duration) error {
	if err := checkURL(raw, "rtsp", "rtsps", "rtmp", "http", "https", "srt", "udp", "rtp"); err != nil {
		return err
	}
	return k.show(ctx, target{mode: ModeStream, url: raw}, d, 0)
}

// StopStream leaves a showing stream. It returns to the base view, or to the
// default view (or welcome page) when the stream is the base view itself.
func (k *Kiosk) StopStream(ctx context.Context) error {
	k.mu.Lock()
	streaming := k.state.Mode == ModeStream
	baseIsStream := k.base.mode == ModeStream
	k.mu.Unlock()
	if !streaming {
		return nil
	}
	if !baseIsStream {
		return k.returnToBase(ctx)
	}
	if def := k.cfg.Get().DefaultView; def != "" {
		if v, ok := k.cfg.Get().View(def); ok && v.Type == config.ViewWeb {
			return k.ShowView(ctx, def, 0)
		}
	}
	if k.welcome != "" {
		k.showWelcome(ctx)
		return nil
	}
	k.player.Stop()
	k.mu.Lock()
	k.base = target{mode: ModeIdle}
	k.mu.Unlock()
	return k.restoreBase(ctx)
}

func (k *Kiosk) show(ctx context.Context, t target, d time.Duration, refreshSeconds int) error {
	if d < 0 {
		return errors.New("duration must not be negative")
	}
	k.mu.Lock()
	if k.revert != nil {
		k.revert.Stop()
		k.revert = nil
	}
	// A permanent view ends the rotation (it can be resumed); a temporary
	// one pauses it until the override expires.
	stoppedRotation := ""
	if d == 0 {
		k.base = t
		if k.rot.active {
			stoppedRotation = k.rot.name
		}
		k.rot.active = false
	}
	k.stopRotationTimerLocked()
	k.mu.Unlock()
	if stoppedRotation != "" {
		k.Emit("rotation_stopped", stoppedRotation)
	}

	if err := k.present(ctx, t, refreshSeconds); err != nil {
		return err
	}

	k.mu.Lock()
	k.state.RevertAt = time.Time{}
	if d > 0 {
		k.state.RevertAt = time.Now().Add(d)
		k.revert = time.AfterFunc(d, func() {
			k.Emit("reverted", "")
			if err := k.returnToBase(context.Background()); err != nil {
				k.log.Warn("revert", "err", err)
			}
		})
	}
	k.mu.Unlock()

	what := t.view
	if what == "" {
		what = player.Redact(t.url)
	}
	if d > 0 {
		what += fmt.Sprintf(" for %s", d)
	}
	k.Emit("show_"+string(t.mode), what)

	if k.cfg.Get().Sleep.WakeOnCommand {
		k.wake(ctx, "command")
	} else {
		k.touch()
	}
	k.publish()
	return nil
}

func (k *Kiosk) present(ctx context.Context, t target, refreshSeconds int) error {
	k.endWebCastIfReplaced(t)
	switch t.mode {
	case ModeStream:
		if err := k.player.Play(t.url); err != nil {
			return err
		}
	case ModeWeb:
		k.player.Stop()
		if err := k.browser.Show(ctx, t.page); err != nil {
			return err
		}
	default:
		k.player.Stop()
	}
	k.mu.Lock()
	k.state.Mode = t.mode
	k.state.View = t.view
	k.state.URL = player.Redact(t.url)
	if k.refresh != nil {
		k.refresh()
		k.refresh = nil
	}
	if t.mode == ModeWeb && refreshSeconds > 0 {
		rctx, cancel := context.WithCancel(context.Background())
		k.refresh = cancel
		go func() {
			tick := time.NewTicker(time.Duration(refreshSeconds) * time.Second)
			defer tick.Stop()
			for {
				select {
				case <-rctx.Done():
					return
				case <-tick.C:
					_ = k.browser.Reload(rctx)
				}
			}
		}()
	}
	k.mu.Unlock()
	return nil
}

func (k *Kiosk) restoreBase(ctx context.Context) error {
	k.mu.Lock()
	if k.revert != nil {
		k.revert.Stop()
		k.revert = nil
	}
	base := k.base
	k.state.RevertAt = time.Time{}
	k.mu.Unlock()

	refresh := 0
	if base.view != "" {
		if v, ok := k.cfg.Get().View(base.view); ok {
			refresh = v.RefreshSeconds
		}
	}
	err := k.present(ctx, base, refresh)
	k.publish()
	return err
}

func (k *Kiosk) streamEnded(u string, err error) {
	k.mu.Lock()
	base := k.base
	k.mu.Unlock()
	k.log.Info("stream stopped by itself", "err", err)
	if base.mode == ModeStream && base.url == u {
		// The base view is this stream: retry rather than fall back to nothing.
		time.AfterFunc(5*time.Second, func() {
			k.mu.Lock()
			still := k.base.url == u && k.state.Mode == ModeStream
			k.mu.Unlock()
			if still {
				if _, playing := k.player.Playing(); !playing {
					_ = k.player.Play(u)
				}
			}
		})
		return
	}
	k.Emit("stream_ended", player.Redact(u))
	if rerr := k.returnToBase(context.Background()); rerr != nil {
		k.log.Warn("restore after stream", "err", rerr)
	}
}

func (k *Kiosk) Reload(ctx context.Context) error {
	k.touch()
	return k.browser.Reload(ctx)
}

func (k *Kiosk) RestartBrowser() {
	k.touch()
	k.Emit("browser_restart", "requested")
	k.browser.Restart()
}

// SetScreen turns the display on or off on request.
func (k *Kiosk) SetScreen(ctx context.Context, on bool) error {
	if err := k.display.SetPower(ctx, on); err != nil {
		return err
	}
	k.mu.Lock()
	k.state.Screen = on
	k.state.ScreenReason = "manual"
	k.state.LastActivity = time.Now()
	k.mu.Unlock()
	k.Emit("screen", onOff(on)+" (manual)")
	k.publish()
	return nil
}

func (k *Kiosk) SetBrightness(ctx context.Context, percent int) error {
	if err := k.display.SetBrightness(ctx, percent); err != nil {
		return err
	}
	k.mu.Lock()
	k.state.Brightness = percent
	k.mu.Unlock()
	k.publish()
	return nil
}

// SetCasting records an AirPlay client connecting or leaving. The
// receiver's window covers the screen while it is connected.
func (k *Kiosk) SetCasting(ctx context.Context, active bool) {
	k.mu.Lock()
	changed := k.airplay != active
	k.airplay = active
	k.updateCastStateLocked()
	k.mu.Unlock()
	if !changed {
		return
	}
	if active {
		k.Emit("cast_started", "AirPlay")
		if k.cfg.Get().Sleep.WakeOnCommand {
			k.wake(ctx, "cast")
		}
	} else {
		k.Emit("cast_ended", "AirPlay")
	}
	k.touch()
	k.publish()
}

func (k *Kiosk) updateCastStateLocked() {
	k.state.Casting = k.airplay || k.webCast != ""
	switch {
	case k.webCast != "":
		k.state.CastSource = k.webCast
	case k.airplay:
		k.state.CastSource = "airplay"
	default:
		k.state.CastSource = ""
	}
}

// webCastMax ends a web cast that nobody stopped.
const webCastMax = 4 * time.Hour

// WebCastIdle ends a web cast whose page has not been playing for this long.
var WebCastIdle = 2 * time.Minute

// WebCast is a page shown on behalf of a phone, such as YouTube's TV app.
type WebCast struct {
	Source    string
	URL       string
	UserAgent string
	// Playing reports from the page URL whether media is playing.
	Playing func(url string) bool
	// Idle ends the cast after this long with nothing playing; 0 uses
	// WebCastIdle.
	Idle time.Duration
	// Keep leaves the page loaded in the background after the cast.
	Keep bool
}

// Page is the browser page a web cast shows.
func (c WebCast) Page() browser.Page {
	return browser.Page{URL: c.URL, UserAgent: c.UserAgent, Keep: c.Keep}
}

// StartWebCast shows a cast page over whatever is showing; the rotation
// pauses. It ends with StopWebCast, when the page stops playing for the
// idle time, or when something else is shown.
func (k *Kiosk) StartWebCast(ctx context.Context, c WebCast) error {
	source, url, playing := c.Source, c.URL, c.Playing
	idle := c.Idle
	if idle == 0 {
		idle = WebCastIdle
	}
	// State and events show the address without the phone's pairing code.
	shown, _, _ := strings.Cut(url, "?")
	t := target{mode: ModeWeb, url: shown, page: c.Page(), cast: source}
	if err := k.show(ctx, t, webCastMax, 0); err != nil {
		return err
	}
	k.mu.Lock()
	fresh := k.webCast == ""
	k.webCast = source
	k.updateCastStateLocked()
	if k.castIdle != nil {
		k.castIdle()
	}
	ictx, cancel := context.WithCancel(context.Background())
	k.castIdle = cancel
	k.mu.Unlock()
	if fresh {
		k.Emit("cast_started", source)
	}
	k.publish()
	go k.watchWebCast(ictx, playing, idle)
	return nil
}

func (k *Kiosk) watchWebCast(ctx context.Context, playing func(string) bool, idle time.Duration) {
	last := time.Now()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		u, err := k.browser.CurrentURL(cctx)
		cancel()
		if err == nil && playing(u) {
			last = time.Now()
			continue
		}
		if time.Since(last) > idle {
			k.Emit("cast_idle", "nothing played for "+idle.String())
			_ = k.StopWebCast(context.Background())
			return
		}
	}
}

// WebCasting returns the source of the running web cast, or "".
func (k *Kiosk) WebCasting() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.webCast
}

// StopWebCast ends the web cast and goes back to what was showing.
func (k *Kiosk) StopWebCast(ctx context.Context) error {
	k.mu.Lock()
	if k.webCast == "" {
		k.mu.Unlock()
		return nil
	}
	k.mu.Unlock()
	return k.returnToBase(ctx)
}

// endWebCastIfReplaced clears the web cast when something else is shown.
func (k *Kiosk) endWebCastIfReplaced(t target) {
	k.mu.Lock()
	src := k.webCast
	if src == "" || t.cast == src {
		k.mu.Unlock()
		return
	}
	k.webCast = ""
	if k.castIdle != nil {
		k.castIdle()
		k.castIdle = nil
	}
	k.updateCastStateLocked()
	k.mu.Unlock()
	k.Emit("cast_ended", src)
}

// Activity records user input on the touchscreen, mouse or keyboard.
func (k *Kiosk) Activity(ctx context.Context) {
	if k.cfg.Get().Sleep.WakeOnInput {
		k.wake(ctx, "input")
		k.publish()
		return
	}
	k.touch()
}

func (k *Kiosk) touch() {
	k.mu.Lock()
	k.state.LastActivity = time.Now()
	k.mu.Unlock()
}

func (k *Kiosk) wake(ctx context.Context, reason string) {
	k.mu.Lock()
	k.state.LastActivity = time.Now()
	off := !k.state.Screen
	k.mu.Unlock()
	if !off {
		return
	}
	if err := k.display.SetPower(ctx, true); err != nil {
		k.log.Warn("wake", "err", err)
		return
	}
	k.mu.Lock()
	k.state.Screen = true
	k.state.ScreenReason = reason
	k.mu.Unlock()
	k.Emit("screen", "on ("+reason+")")
}

func (k *Kiosk) sleepScreen(ctx context.Context, reason string) {
	if err := k.display.SetPower(ctx, false); err != nil {
		k.log.Warn("sleep", "err", err)
		return
	}
	k.mu.Lock()
	k.state.Screen = false
	k.state.ScreenReason = reason
	k.mu.Unlock()
	k.Emit("screen", "off ("+reason+")")
	k.publish()
}

// checkSleep applies the schedule on its transitions and the idle timeout.
// Acting only on transitions lets a manual or command wake override the
// schedule until the next boundary.
func (k *Kiosk) checkSleep(ctx context.Context, now time.Time) {
	s := k.cfg.Get().Sleep
	if s.ScheduleEnabled {
		off := InOffWindow(s, now)
		k.mu.Lock()
		changed := k.schedOff == nil || *k.schedOff != off
		k.schedOff = &off
		screen := k.state.Screen
		k.mu.Unlock()
		if changed {
			switch {
			case off && screen:
				k.sleepScreen(ctx, "schedule")
			case !off && !screen:
				k.wake(ctx, "schedule")
				k.publish()
			}
			return
		}
	} else {
		k.mu.Lock()
		k.schedOff = nil
		k.mu.Unlock()
	}
	if s.IdleMinutes > 0 {
		k.mu.Lock()
		idle := k.state.Screen && now.Sub(k.state.LastActivity) >= time.Duration(s.IdleMinutes)*time.Minute
		k.mu.Unlock()
		if idle {
			k.sleepScreen(ctx, "idle")
		}
	}
}

// InOffWindow reports whether now falls in the schedule's screen-off period.
// The period may cross midnight; weekdays select the day the period starts.
func InOffWindow(s config.Sleep, now time.Time) bool {
	offAt, err1 := config.ParseClock(s.ScheduleOff)
	onAt, err2 := config.ParseClock(s.ScheduleOn)
	if err1 != nil || err2 != nil || offAt == onAt {
		return false
	}
	minute := now.Hour()*60 + now.Minute()
	day := int(now.Weekday())
	dayOK := func(d int) bool { return len(s.ScheduleWeekdays) == 0 || slices.Contains(s.ScheduleWeekdays, d) }
	if offAt < onAt {
		return minute >= offAt && minute < onAt && dayOK(day)
	}
	// Crosses midnight, e.g. 22:00 → 07:00.
	if minute >= offAt {
		return dayOK(day)
	}
	if minute < onAt {
		return dayOK((day + 6) % 7)
	}
	return false
}

func (k *Kiosk) configChanged(old, cfg config.Config) {
	ctx := context.Background()
	if old.Display != cfg.Display {
		_ = k.applyDisplay(ctx, cfg)
	}
	k.mu.Lock()
	base := k.base
	rotActive, rotName := k.rot.active, k.rot.name
	k.mu.Unlock()

	if rotActive {
		r, ok := cfg.Rotation(rotName)
		if !ok {
			k.StopRotation()
		} else {
			k.mu.Lock()
			k.rot.items = r.Items
			if k.rot.idx >= len(r.Items) {
				k.rot.idx = 0
			}
			k.mu.Unlock()
			oldV, _ := old.View(base.view)
			if newV, _ := cfg.View(base.view); !oldV.Equal(newV) {
				_ = k.rotationShow(ctx)
			}
			k.publish()
			return
		}
	}
	if cfg.DefaultRotation != "" && old.DefaultRotation == "" && (base.view == "" || base.url == k.welcome) {
		_ = k.StartRotation(ctx, cfg.DefaultRotation)
		k.publish()
		return
	}
	// Re-show the base view if its definition changed or it was removed.
	if base.view != "" {
		oldV, _ := old.View(base.view)
		newV, ok := cfg.View(base.view)
		switch {
		case !ok && cfg.DefaultView != "":
			_ = k.ShowView(ctx, cfg.DefaultView, 0)
		case ok && !oldV.Equal(newV):
			_ = k.ShowView(ctx, base.view, 0)
		}
	} else if cfg.DefaultView != "" && old.DefaultView == "" {
		_ = k.ShowView(ctx, cfg.DefaultView, 0)
	} else if cfg.DefaultView == "" && old.DefaultView != "" && base.view == "" {
		k.showWelcome(ctx)
	}
	k.publish()
}

// reapplyScreen restores the screen power state after the compositor
// (re)starts, since a new sway session powers outputs on.
func (k *Kiosk) reapplyScreen(ctx context.Context) {
	k.mu.Lock()
	on := k.state.Screen
	k.mu.Unlock()
	if !on {
		_ = k.display.SetPower(ctx, false)
	}
}

func (k *Kiosk) showWelcome(ctx context.Context) {
	if k.welcome == "" {
		return
	}
	if err := k.show(ctx, target{mode: ModeWeb, url: k.welcome, page: browser.Page{URL: k.welcome}}, 0, 0); err != nil {
		k.log.Warn("welcome page", "err", err)
	}
}

func (k *Kiosk) applyDisplay(ctx context.Context, cfg config.Config) error {
	d := cfg.Display
	err := errors.Join(
		k.display.SetRotation(ctx, d.Rotation),
		k.display.SetScale(ctx, d.Scale),
		k.display.SetHideCursor(ctx, d.HideCursorSeconds),
	)
	if d.Brightness >= 0 {
		if berr := k.display.SetBrightness(ctx, d.Brightness); berr != nil {
			k.log.Debug("brightness", "err", berr)
		} else {
			k.mu.Lock()
			k.state.Brightness = d.Brightness
			k.mu.Unlock()
		}
	}
	return err
}

func (k *Kiosk) publish() {
	k.mu.Lock()
	k.state.Rotation = k.rot.name
	k.state.RotationActive = k.rot.active
	k.state.RotationIndex = k.rot.idx
	st := k.state
	ls := append([]func(State){}, k.listeners...)
	k.mu.Unlock()
	for _, fn := range ls {
		fn(st)
	}
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func checkURL(raw string, schemes ...string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if !slices.Contains(schemes, u.Scheme) || u.Host == "" {
		return fmt.Errorf("URL must start with one of %v://", schemes)
	}
	return nil
}
