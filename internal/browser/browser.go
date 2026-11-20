// Package browser runs Chromium in kiosk mode and drives it over the
// DevTools protocol on a loopback port.
package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/XtremeOwnage/pikiosk/internal/display"
)

type Options struct {
	Binary     string
	DataDir    string
	Port       int
	ExtraFlags []string
	RuntimeDir string // XDG_RUNTIME_DIR holding the compositor's socket
}

// Page describes what the browser should show.
type Page struct {
	URL string
	// HAToken is stored where the Home Assistant frontend reads its tokens.
	HAToken string
	// Headers are added to requests to the page's origin, including the
	// Authorization header for basic or bearer auth.
	Headers map[string]string
	CSS     string
	Zoom    float64
	// Keep leaves the page loaded in a background tab after another page is
	// shown, so coming back to it is instant and it stays live.
	Keep bool
	// UserAgent overrides the browser's user agent for this page.
	UserAgent string
}

// same reports whether a and b load the same thing; Keep does not count.
func (a Page) same(b Page) bool {
	a.Keep, b.Keep = false, false
	return reflect.DeepEqual(a, b)
}

// MaxBackgroundTabs bounds the tabs kept loaded behind the visible one.
const MaxBackgroundTabs = 3

// tab is one Chromium page target with its own DevTools connection.
type tab struct {
	id      string
	conn    *cdp
	page    Page
	loaded  bool
	ua      string // user agent override in effect
	scripts []string
	crashed chan struct{}
	used    time.Time
}

type Browser struct {
	opts Options
	log  *slog.Logger

	// switchMu serialises Show and Preload, which create and swap tabs.
	switchMu sync.Mutex

	mu        sync.Mutex
	front     *tab
	swapped   chan struct{}
	defaultUA string
	bg        []*tab
	proc      *os.Process
	page      Page
	running   bool
	lastErr   string
	restarts  int
}

func New(opts Options, log *slog.Logger) *Browser {
	if opts.Binary == "" {
		opts.Binary = "chromium"
	}
	if opts.Port == 0 {
		opts.Port = 9222
	}
	return &Browser{opts: opts, log: log.With("component", "browser"), swapped: make(chan struct{}, 1)}
}

type Status struct {
	Running        bool   `json:"running"`
	Connected      bool   `json:"connected"`
	Restarts       int    `json:"restarts"`
	BackgroundTabs int    `json:"background_tabs"`
	LastError      string `json:"last_error,omitempty"`
}

func (b *Browser) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Status{Running: b.running, Connected: b.front != nil, Restarts: b.restarts, BackgroundTabs: len(b.bg), LastError: b.lastErr}
}

// Run supervises Chromium until ctx is cancelled.
func (b *Browser) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		wayland, ok := display.WaylandDisplay(b.opts.RuntimeDir)
		if !ok {
			b.setErr("waiting for the display in " + b.opts.RuntimeDir)
			sleep(ctx, 2*time.Second)
			continue
		}
		start := time.Now()
		if err := b.runOnce(ctx, wayland); err != nil && ctx.Err() == nil {
			b.log.Warn("chromium exited", "err", err)
			b.setErr(err.Error())
		}
		b.mu.Lock()
		b.restarts++
		b.mu.Unlock()
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		sleep(ctx, backoff)
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (b *Browser) runOnce(ctx context.Context, wayland string) error {
	if err := os.MkdirAll(b.opts.DataDir, 0o700); err != nil {
		return err
	}
	markCleanExit(b.opts.DataDir)

	args := append([]string{
		"--ozone-platform=wayland",
		"--kiosk",
		"--no-first-run",
		"--no-default-browser-check",
		"--noerrdialogs",
		"--disable-infobars",
		"--disable-session-crashed-bubble",
		"--hide-crash-restore-bubble",
		"--disable-pinch",
		"--overscroll-history-navigation=0",
		"--disable-background-networking",
		"--disable-component-update",
		"--disable-sync",
		"--disable-default-apps",
		"--disable-breakpad",
		"--disable-domain-reliability",
		"--disable-features=Translate,OptimizationHints,MediaRouter,DialMediaRouteProvider,InterestFeedContentSuggestions,AutofillServerCommunication,CertificateTransparencyComponentUpdater",
		// Background tabs hold preloaded and kept pages; keep them running.
		"--disable-background-timer-throttling",
		"--disable-renderer-backgrounding",
		"--disable-backgrounding-occluded-windows",
		"--check-for-update-interval=31536000",
		"--password-store=basic",
		"--autoplay-policy=no-user-gesture-required",
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=" + strconv.Itoa(b.opts.Port),
		"--user-data-dir=" + b.opts.DataDir,
	}, b.opts.ExtraFlags...)
	args = append(args, "about:blank")

	cmd := exec.CommandContext(ctx, b.opts.Binary, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(), "WAYLAND_DISPLAY="+wayland, "XDG_RUNTIME_DIR="+b.opts.RuntimeDir)
	if err := cmd.Start(); err != nil {
		return err
	}
	b.mu.Lock()
	b.proc = cmd.Process
	b.running = true
	b.mu.Unlock()
	b.log.Info("chromium started", "pid", cmd.Process.Pid)

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	connCtx, stopConn := context.WithCancel(ctx)
	go b.connectLoop(connCtx)

	err := <-exited
	stopConn()
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	b.mu.Lock()
	b.running = false
	b.proc = nil
	b.mu.Unlock()
	b.dropTabs()
	if err == nil {
		err = errors.New("exited")
	}
	return err
}

// dropTabs forgets every tab and closes their connections.
func (b *Browser) dropTabs() {
	b.mu.Lock()
	tabs := b.bg
	if b.front != nil {
		tabs = append(tabs, b.front)
	}
	b.front, b.bg = nil, nil
	b.mu.Unlock()
	for _, t := range tabs {
		t.conn.Close()
	}
}

// attach opens a DevTools connection to a page target.
func (b *Browser) attach(ctx context.Context, id, wsURL string) (*tab, error) {
	t := &tab{id: id, crashed: make(chan struct{}, 1), used: time.Now()}
	ready := make(chan struct{})
	conn, err := dial(ctx, wsURL, func(method string, params json.RawMessage) {
		switch method {
		case "Inspector.targetCrashed":
			select {
			case t.crashed <- struct{}{}:
			default:
			}
		case "Fetch.requestPaused":
			// Calls block until readLoop delivers the reply.
			go func() {
				<-ready
				b.continueRequest(ctx, t, params)
			}()
		}
	})
	if err != nil {
		return nil, err
	}
	t.conn = conn
	close(ready)
	// Scripts added for new documents only run with the Page domain on.
	if err := conn.Call(ctx, "Page.enable", nil, nil); err != nil {
		conn.Close()
		return nil, err
	}
	return t, nil
}

// connectLoop keeps a DevTools connection to the visible page and replays
// the desired page whenever a new connection is made.
func (b *Browser) connectLoop(ctx context.Context) {
	for ctx.Err() == nil {
		id, wsURL, err := findPage(ctx, b.opts.Port)
		if err != nil {
			sleep(ctx, 500*time.Millisecond)
			continue
		}
		t, err := b.attach(ctx, id, wsURL)
		if err != nil {
			sleep(ctx, time.Second)
			continue
		}
		b.mu.Lock()
		b.front = t
		page := b.page
		b.lastErr = ""
		b.mu.Unlock()
		b.log.Info("devtools connected")
		b.closeOthers(ctx, t.id)

		if page.URL != "" {
			if err := b.apply(ctx, t, page); err != nil {
				b.log.Warn("restore page failed", "err", err)
			}
		}
		b.watch(ctx, t)
		b.dropTabs()
		sleep(ctx, 500*time.Millisecond)
	}
}

// watch returns when the visible tab's connection drops. It reloads a
// crashed page, drops crashed background tabs, and restarts Chromium when
// the page stops answering.
func (b *Browser) watch(ctx context.Context, first *tab) {
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	failures := 0
	for {
		b.mu.Lock()
		t := b.front
		b.mu.Unlock()
		if t == nil {
			t = first
		}
		select {
		case <-t.conn.Done():
			b.mu.Lock()
			still := b.front == t
			b.mu.Unlock()
			if still {
				return
			}
		case <-ctx.Done():
			return
		case <-b.swapped:
			continue
		case <-t.crashed:
			b.log.Warn("page crashed, reloading")
			b.setErr("page crashed")
			b.mu.Lock()
			page := b.page
			b.mu.Unlock()
			if page.URL != "" {
				_ = b.apply(ctx, t, page)
			}
		case <-tick.C:
			b.dropCrashedBackground()
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := t.conn.Call(pctx, "Runtime.evaluate", map[string]any{"expression": "1", "returnByValue": true}, nil)
			cancel()
			if err == nil {
				failures = 0
				continue
			}
			failures++
			b.log.Warn("browser not responding", "failures", failures, "err", err)
			if failures >= 3 {
				b.setErr("not responding; restarting")
				b.Restart()
				return
			}
		}
	}
}

func (b *Browser) dropCrashedBackground() {
	b.mu.Lock()
	var keep, drop []*tab
	for _, t := range b.bg {
		select {
		case <-t.crashed:
			drop = append(drop, t)
			continue
		case <-t.conn.Done():
			drop = append(drop, t)
			continue
		default:
		}
		keep = append(keep, t)
	}
	b.bg = keep
	b.mu.Unlock()
	for _, t := range drop {
		b.log.Warn("background tab lost", "url", t.page.URL)
		b.closeTab(context.Background(), t)
	}
}

// Show makes page visible. A page already loaded in a background tab is
// brought to the front without reloading; the page that was showing stays
// loaded behind it if it is kept. Showing the page that is already visible
// does nothing.
func (b *Browser) Show(ctx context.Context, page Page) error {
	b.switchMu.Lock()
	defer b.switchMu.Unlock()
	b.mu.Lock()
	b.page = page
	front := b.front
	if front == nil {
		b.mu.Unlock()
		return nil // applied when the browser connects
	}
	if front.loaded && front.page.same(page) {
		front.page.Keep = page.Keep
		front.used = time.Now()
		b.mu.Unlock()
		return nil
	}
	var next *tab
	for i, t := range b.bg {
		if t.page.same(page) {
			next = t
			b.bg = append(b.bg[:i:i], b.bg[i+1:]...)
			break
		}
	}
	b.mu.Unlock()

	if next == nil && !front.page.Keep {
		return b.apply(ctx, front, page)
	}
	if next == nil {
		// Keep the current page loaded: open the new one in another tab.
		t, err := b.newTab(ctx)
		if err != nil {
			b.log.Warn("new tab failed; navigating in place", "err", err)
			return b.apply(ctx, front, page)
		}
		if err := b.apply(ctx, t, page); err != nil {
			b.closeTab(ctx, t)
			return err
		}
		next = t
	}
	next.page.Keep = page.Keep
	if err := b.activate(ctx, next); err != nil {
		b.closeTab(ctx, next)
		b.log.Warn("activate tab failed; navigating in place", "err", err)
		return b.apply(ctx, front, page)
	}
	next.used = time.Now()
	b.mu.Lock()
	b.front = next
	select {
	case b.swapped <- struct{}{}:
	default:
	}
	if front.page.Keep {
		front.used = time.Now()
		b.bg = append(b.bg, front)
		front = nil
	}
	evict := b.evictLocked()
	b.mu.Unlock()
	if front != nil {
		b.closeTab(ctx, front)
	}
	for _, t := range evict {
		b.closeTab(ctx, t)
	}
	return nil
}

// Preload loads page in a background tab so a later Show is instant. Only
// one page that is not kept is preloaded at a time.
func (b *Browser) Preload(ctx context.Context, page Page) error {
	b.switchMu.Lock()
	defer b.switchMu.Unlock()
	b.mu.Lock()
	if b.front == nil || b.front.page.same(page) {
		b.mu.Unlock()
		return nil
	}
	for _, t := range b.bg {
		if t.page.same(page) {
			t.used = time.Now()
			t.page.Keep = t.page.Keep || page.Keep
			b.mu.Unlock()
			return nil
		}
	}
	var stale, keep []*tab
	for _, t := range b.bg {
		if !t.page.Keep && !page.Keep {
			stale = append(stale, t)
		} else {
			keep = append(keep, t)
		}
	}
	b.bg = keep
	b.mu.Unlock()
	for _, t := range stale {
		b.closeTab(ctx, t)
	}

	t, err := b.newTab(ctx)
	if err != nil {
		return err
	}
	if err := b.apply(ctx, t, page); err != nil {
		b.closeTab(ctx, t)
		return err
	}
	b.mu.Lock()
	b.bg = append(b.bg, t)
	evict := b.evictLocked()
	b.mu.Unlock()
	for _, e := range evict {
		b.closeTab(ctx, e)
	}
	b.log.Debug("preloaded", "url", page.URL)
	return nil
}

// evictLocked trims background tabs to MaxBackgroundTabs, least recently
// used first, preferring pages that are not kept.
func (b *Browser) evictLocked() []*tab {
	var out []*tab
	for len(b.bg) > MaxBackgroundTabs {
		victim := -1
		for i, t := range b.bg {
			if victim < 0 || (!t.page.Keep && b.bg[victim].page.Keep) ||
				(t.page.Keep == b.bg[victim].page.Keep && t.used.Before(b.bg[victim].used)) {
				victim = i
			}
		}
		out = append(out, b.bg[victim])
		b.bg = append(b.bg[:victim:victim], b.bg[victim+1:]...)
	}
	return out
}

// newTab opens a page target in the background.
func (b *Browser) newTab(ctx context.Context) (*tab, error) {
	var res struct {
		TargetID string `json:"targetId"`
	}
	if err := b.browserCall(ctx, "Target.createTarget", map[string]any{"url": "about:blank", "background": true}, &res); err != nil {
		return nil, err
	}
	ws := fmt.Sprintf("ws://127.0.0.1:%d/devtools/page/%s", b.opts.Port, res.TargetID)
	t, err := b.attach(ctx, res.TargetID, ws)
	if err != nil {
		_ = b.browserCall(ctx, "Target.closeTarget", map[string]any{"targetId": res.TargetID}, nil)
		return nil, err
	}
	return t, nil
}

func (b *Browser) activate(ctx context.Context, t *tab) error {
	return b.browserCall(ctx, "Target.activateTarget", map[string]any{"targetId": t.id}, nil)
}

func (b *Browser) closeTab(ctx context.Context, t *tab) {
	t.conn.Close()
	if t.id != "" {
		_ = b.browserCall(ctx, "Target.closeTarget", map[string]any{"targetId": t.id}, nil)
	}
}

// defaultUserAgent is Chromium's own user agent, used to undo an override.
func (b *Browser) defaultUserAgent(ctx context.Context) (string, error) {
	b.mu.Lock()
	ua := b.defaultUA
	b.mu.Unlock()
	if ua != "" {
		return ua, nil
	}
	var res struct {
		UserAgent string `json:"userAgent"`
	}
	if err := b.browserCall(ctx, "Browser.getVersion", nil, &res); err != nil {
		return "", err
	}
	b.mu.Lock()
	b.defaultUA = res.UserAgent
	b.mu.Unlock()
	return res.UserAgent, nil
}

// closeOthers closes page targets left over from an earlier connection, so
// the attached tab is the only one and therefore the visible one.
func (b *Browser) closeOthers(ctx context.Context, keepID string) {
	ids, err := pageIDs(ctx, b.opts.Port)
	if err != nil {
		return
	}
	for _, id := range ids {
		if id != keepID && id != "" {
			_ = b.browserCall(ctx, "Target.closeTarget", map[string]any{"targetId": id}, nil)
		}
	}
}

// browserCall runs one command on the browser-level DevTools endpoint.
func (b *Browser) browserCall(ctx context.Context, method string, params, out any) error {
	ws, err := browserEndpoint(ctx, b.opts.Port)
	if err != nil {
		return err
	}
	conn, err := dial(ctx, ws, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Call(ctx, method, params, out)
}

func (b *Browser) apply(ctx context.Context, t *tab, page Page) error {
	conn := t.conn
	b.mu.Lock()
	old := t.scripts
	t.scripts = nil
	t.page = page
	t.loaded = false
	b.mu.Unlock()
	for _, id := range old {
		_ = conn.Call(ctx, "Page.removeScriptToEvaluateOnNewDocument", map[string]any{"identifier": id}, nil)
	}

	if page.UserAgent != t.ua {
		ua := page.UserAgent
		if ua == "" {
			def, err := b.defaultUserAgent(ctx)
			if err != nil {
				return err
			}
			ua = def
		}
		if err := conn.Call(ctx, "Emulation.setUserAgentOverride", map[string]any{"userAgent": ua}, nil); err != nil {
			return err
		}
		t.ua = page.UserAgent
	}

	if len(page.Headers) > 0 {
		origin, err := originOf(page.URL)
		if err != nil {
			return err
		}
		if err := conn.Call(ctx, "Fetch.enable", map[string]any{
			"patterns": []map[string]any{{"urlPattern": origin + "/*", "requestStage": "Request"}},
		}, nil); err != nil {
			return err
		}
	} else if err := conn.Call(ctx, "Fetch.disable", nil, nil); err != nil {
		return err
	}

	var scripts []string
	if page.CSS != "" {
		css, _ := json.Marshal(page.CSS)
		scripts = append(scripts, fmt.Sprintf(
			`(()=>{const add=()=>{const s=document.createElement("style");s.dataset.pikiosk="";s.textContent=%s;(document.head||document.documentElement).appendChild(s)};if(document.readyState==="loading")document.addEventListener("DOMContentLoaded",add);else add()})();`, css))
	}
	if page.HAToken != "" {
		js, err := haTokenScript(page.URL, page.HAToken)
		if err != nil {
			return err
		}
		scripts = append(scripts, js)
	}
	if page.Zoom > 0 && page.Zoom != 1 {
		scripts = append(scripts, fmt.Sprintf(
			`document.addEventListener("DOMContentLoaded",()=>{document.documentElement.style.zoom=%q});`,
			strconv.FormatFloat(page.Zoom, 'f', -1, 64)))
	}
	var ids []string
	for _, js := range scripts {
		var res struct {
			Identifier string `json:"identifier"`
		}
		if err := conn.Call(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": js}, &res); err != nil {
			return err
		}
		ids = append(ids, res.Identifier)
	}
	b.mu.Lock()
	t.scripts = ids
	b.mu.Unlock()

	var res struct {
		ErrorText string `json:"errorText"`
	}
	if err := conn.Call(ctx, "Page.navigate", map[string]any{"url": page.URL}, &res); err != nil {
		return err
	}
	if res.ErrorText != "" {
		return fmt.Errorf("navigate %s: %s", page.URL, res.ErrorText)
	}
	b.mu.Lock()
	t.loaded = true
	b.mu.Unlock()
	return nil
}

// continueRequest adds the tab's headers to a paused request. Fetch only
// pauses requests to the page's origin, so credentials stay there.
func (b *Browser) continueRequest(ctx context.Context, t *tab, params json.RawMessage) {
	var ev struct {
		RequestID string `json:"requestId"`
		Request   struct {
			Headers map[string]string `json:"headers"`
		} `json:"request"`
	}
	if json.Unmarshal(params, &ev) != nil || ev.RequestID == "" {
		return
	}
	b.mu.Lock()
	extra := t.page.Headers
	b.mu.Unlock()
	merged := map[string]string{}
	for k, v := range ev.Request.Headers {
		merged[http.CanonicalHeaderKey(k)] = v
	}
	for k, v := range extra {
		merged[http.CanonicalHeaderKey(k)] = v
	}
	hdrs := make([]map[string]string, 0, len(merged))
	for k, v := range merged {
		hdrs = append(hdrs, map[string]string{"name": k, "value": v})
	}
	if err := t.conn.Call(ctx, "Fetch.continueRequest", map[string]any{"requestId": ev.RequestID, "headers": hdrs}, nil); err != nil {
		b.log.Debug("continue request", "err", err)
	}
}

// haTokenScript stores a long-lived token where the Home Assistant frontend
// looks for its auth tokens, scoped to the dashboard's origin.
func haTokenScript(rawURL, token string) (string, error) {
	origin, err := originOf(rawURL)
	if err != nil {
		return "", err
	}
	o, _ := json.Marshal(origin)
	t, _ := json.Marshal(token)
	return fmt.Sprintf(`(()=>{try{
const o=%s,t=%s;
if(location.origin!==o)return;
const cur=JSON.parse(localStorage.getItem("hassTokens")||"null");
if(cur&&cur.access_token===t)return;
localStorage.setItem("hassTokens",JSON.stringify({hassUrl:o,clientId:o+"/",access_token:t,token_type:"Bearer",expires_in:1800,refresh_token:"",expires:32503680000000}));
}catch(e){}})();`, o, t), nil
}

func (b *Browser) Reload(ctx context.Context) error {
	conn, err := b.connection()
	if err != nil {
		return err
	}
	return conn.Call(ctx, "Page.reload", map[string]any{"ignoreCache": true}, nil)
}

// Restart kills Chromium; the supervisor starts it again.
func (b *Browser) Restart() {
	b.mu.Lock()
	p := b.proc
	b.mu.Unlock()
	if p != nil {
		_ = syscall.Kill(-p.Pid, syscall.SIGTERM)
	}
}

func (b *Browser) Screenshot(ctx context.Context, quality int) ([]byte, error) {
	conn, err := b.connection()
	if err != nil {
		return nil, err
	}
	var res struct {
		Data string `json:"data"`
	}
	if err := conn.Call(ctx, "Page.captureScreenshot", map[string]any{"format": "jpeg", "quality": quality}, &res); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(res.Data)
}

// Click taps at a position given as a fraction (0-1) of the viewport.
func (b *Browser) Click(ctx context.Context, fx, fy float64) error {
	conn, err := b.connection()
	if err != nil {
		return err
	}
	x, y, err := viewportPoint(ctx, conn, fx, fy)
	if err != nil {
		return err
	}
	for _, typ := range []string{"mousePressed", "mouseReleased"} {
		if err := conn.Call(ctx, "Input.dispatchMouseEvent", map[string]any{
			"type": typ, "x": x, "y": y, "button": "left", "clickCount": 1,
		}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (b *Browser) Scroll(ctx context.Context, fx, fy, dy float64) error {
	conn, err := b.connection()
	if err != nil {
		return err
	}
	x, y, err := viewportPoint(ctx, conn, fx, fy)
	if err != nil {
		return err
	}
	return conn.Call(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": "mouseWheel", "x": x, "y": y, "deltaX": 0, "deltaY": dy,
	}, nil)
}

func (b *Browser) Type(ctx context.Context, text string) error {
	conn, err := b.connection()
	if err != nil {
		return err
	}
	return conn.Call(ctx, "Input.insertText", map[string]any{"text": text}, nil)
}

var keys = map[string]struct {
	code string
	vk   int
	text string
}{
	"Enter":      {"Enter", 13, "\r"},
	"Tab":        {"Tab", 9, ""},
	"Backspace":  {"Backspace", 8, ""},
	"Escape":     {"Escape", 27, ""},
	"ArrowUp":    {"ArrowUp", 38, ""},
	"ArrowDown":  {"ArrowDown", 40, ""},
	"ArrowLeft":  {"ArrowLeft", 37, ""},
	"ArrowRight": {"ArrowRight", 39, ""},
}

func (b *Browser) Key(ctx context.Context, name string) error {
	conn, err := b.connection()
	if err != nil {
		return err
	}
	return keyOn(ctx, conn, name)
}

func keyOn(ctx context.Context, conn *cdp, name string) error {
	k, ok := keys[name]
	if !ok {
		return fmt.Errorf("unsupported key %q", name)
	}
	down := map[string]any{"type": "rawKeyDown", "key": name, "code": k.code, "windowsVirtualKeyCode": k.vk}
	if k.text != "" {
		down["type"] = "keyDown"
		down["text"] = k.text
	}
	if err := conn.Call(ctx, "Input.dispatchKeyEvent", down, nil); err != nil {
		return err
	}
	return conn.Call(ctx, "Input.dispatchKeyEvent", map[string]any{
		"type": "keyUp", "key": name, "code": k.code, "windowsVirtualKeyCode": k.vk,
	}, nil)
}

// ClearData deletes cookies and site storage, logging out of every site.
func (b *Browser) ClearData(ctx context.Context) error {
	conn, err := b.connection()
	if err != nil {
		return err
	}
	if err := conn.Call(ctx, "Network.clearBrowserCookies", nil, nil); err != nil {
		return err
	}
	// Background tabs still hold the old sessions in memory.
	b.switchMu.Lock()
	b.mu.Lock()
	bg := b.bg
	b.bg = nil
	b.mu.Unlock()
	for _, t := range bg {
		b.closeTab(ctx, t)
	}
	b.switchMu.Unlock()
	return conn.Call(ctx, "Storage.clearDataForOrigin", map[string]any{"origin": "*", "storageTypes": "all"}, nil)
}

func (b *Browser) CurrentURL(ctx context.Context) (string, error) {
	conn, err := b.connection()
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := conn.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true}, &res); err != nil {
		return "", err
	}
	return res.Result.Value, nil
}

// connection returns the visible tab's DevTools connection.
func (b *Browser) connection() (*cdp, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.front == nil {
		return nil, errors.New("browser not connected")
	}
	return b.front.conn, nil
}

// Title returns the visible page's title.
func (b *Browser) Title(ctx context.Context) (string, error) {
	conn, err := b.connection()
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := conn.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "document.title", "returnByValue": true}, &res); err != nil {
		return "", err
	}
	return res.Result.Value, nil
}

func (b *Browser) setErr(s string) {
	b.mu.Lock()
	b.lastErr = s
	b.mu.Unlock()
}

func viewportPoint(ctx context.Context, conn *cdp, fx, fy float64) (float64, float64, error) {
	if fx < 0 || fx > 1 || fy < 0 || fy > 1 {
		return 0, 0, errors.New("position must be between 0 and 1")
	}
	var res struct {
		CSSVisualViewport struct {
			ClientWidth  float64 `json:"clientWidth"`
			ClientHeight float64 `json:"clientHeight"`
		} `json:"cssVisualViewport"`
	}
	if err := conn.Call(ctx, "Page.getLayoutMetrics", nil, &res); err != nil {
		return 0, 0, err
	}
	return fx * res.CSSVisualViewport.ClientWidth, fy * res.CSSVisualViewport.ClientHeight, nil
}

func originOf(raw string) (string, error) {
	i := strings.Index(raw, "://")
	if i < 0 {
		return "", fmt.Errorf("invalid URL %q", raw)
	}
	rest := raw[i+3:]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}
	return raw[:i+3] + rest, nil
}

// markCleanExit stops Chromium offering to restore the previous session
// after a power cut.
func markCleanExit(dataDir string) {
	p := filepath.Join(dataDir, "Default", "Preferences")
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var prefs map[string]any
	if json.Unmarshal(data, &prefs) != nil {
		return
	}
	profile, _ := prefs["profile"].(map[string]any)
	if profile == nil {
		return
	}
	profile["exit_type"] = "Normal"
	profile["exited_cleanly"] = true
	if out, err := json.Marshal(prefs); err == nil {
		_ = os.WriteFile(p, out, 0o600)
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// ErrNoTab means no tab shows the page.
var ErrNoTab = errors.New("page is not loaded")

// tabFor returns the tab, visible or in the background, showing page.
func (b *Browser) tabFor(page Page) (*tab, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.front != nil && b.front.page.same(page) {
		return b.front, true, nil
	}
	for _, t := range b.bg {
		if t.page.same(page) {
			return t, false, nil
		}
	}
	return nil, false, ErrNoTab
}

// Loaded reports whether a tab holds page, and whether it is the visible one.
func (b *Browser) Loaded(page Page) (loaded, visible bool) {
	_, vis, err := b.tabFor(page)
	return err == nil, vis
}

// EvalIn evaluates a JavaScript expression in the tab showing page and
// returns its result as a string.
func (b *Browser) EvalIn(ctx context.Context, page Page, expr string) (string, error) {
	t, _, err := b.tabFor(page)
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := t.conn.Call(ctx, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true}, &res); err != nil {
		return "", err
	}
	if res.ExceptionDetails != nil {
		return "", errors.New(res.ExceptionDetails.Text)
	}
	if s, ok := res.Result.Value.(string); ok {
		return s, nil
	}
	out, _ := json.Marshal(res.Result.Value)
	return string(out), nil
}

// KeyIn presses a key in the tab showing page, visible or not.
func (b *Browser) KeyIn(ctx context.Context, page Page, name string) error {
	t, _, err := b.tabFor(page)
	if err != nil {
		return err
	}
	return keyOn(ctx, t.conn, name)
}

// Drop closes the background tab showing page, if there is one.
func (b *Browser) Drop(ctx context.Context, page Page) {
	b.switchMu.Lock()
	defer b.switchMu.Unlock()
	b.mu.Lock()
	var gone *tab
	for i, t := range b.bg {
		if t.page.same(page) {
			gone = t
			b.bg = append(b.bg[:i:i], b.bg[i+1:]...)
			break
		}
	}
	b.mu.Unlock()
	if gone != nil {
		b.closeTab(ctx, gone)
	}
}
