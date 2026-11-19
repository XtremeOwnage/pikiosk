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
}

type Browser struct {
	opts Options
	log  *slog.Logger

	mu       sync.Mutex
	conn     *cdp
	proc     *os.Process
	page     Page
	scripts  []string
	running  bool
	lastErr  string
	restarts int
}

func New(opts Options, log *slog.Logger) *Browser {
	if opts.Binary == "" {
		opts.Binary = "chromium"
	}
	if opts.Port == 0 {
		opts.Port = 9222
	}
	return &Browser{opts: opts, log: log.With("component", "browser")}
}

type Status struct {
	Running   bool   `json:"running"`
	Connected bool   `json:"connected"`
	Restarts  int    `json:"restarts"`
	LastError string `json:"last_error,omitempty"`
}

func (b *Browser) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Status{Running: b.running, Connected: b.conn != nil, Restarts: b.restarts, LastError: b.lastErr}
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
	if b.conn != nil {
		b.conn.Close()
		b.conn = nil
	}
	b.mu.Unlock()
	if err == nil {
		err = errors.New("exited")
	}
	return err
}

// connectLoop keeps a DevTools connection to the current page target and
// replays the desired page whenever a new connection is made.
func (b *Browser) connectLoop(ctx context.Context) {
	for ctx.Err() == nil {
		wsURL, err := findPage(ctx, b.opts.Port)
		if err != nil {
			sleep(ctx, 500*time.Millisecond)
			continue
		}
		crashed := make(chan struct{}, 1)
		var conn *cdp
		ready := make(chan struct{})
		conn, err = dial(ctx, wsURL, func(method string, params json.RawMessage) {
			switch method {
			case "Inspector.targetCrashed":
				select {
				case crashed <- struct{}{}:
				default:
				}
			case "Fetch.requestPaused":
				// Calls block until readLoop delivers the reply.
				go func() {
					<-ready
					b.continueRequest(ctx, conn, params)
				}()
			}
		})
		if err != nil {
			sleep(ctx, time.Second)
			continue
		}
		b.mu.Lock()
		b.conn = conn
		b.scripts = nil
		page := b.page
		b.lastErr = ""
		b.mu.Unlock()
		close(ready)
		b.log.Info("devtools connected")
		// Scripts added for new documents only run with the Page domain on.
		if err := conn.Call(ctx, "Page.enable", nil, nil); err != nil {
			b.log.Warn("enable page domain", "err", err)
		}

		if page.URL != "" {
			if err := b.apply(ctx, conn, page); err != nil {
				b.log.Warn("restore page failed", "err", err)
			}
		}
		b.watch(ctx, conn, crashed)
		b.mu.Lock()
		if b.conn == conn {
			b.conn = nil
		}
		b.mu.Unlock()
		conn.Close()
		sleep(ctx, 500*time.Millisecond)
	}
}

// watch returns when the connection drops. It reloads a crashed page and
// restarts Chromium when the page stops answering.
func (b *Browser) watch(ctx context.Context, conn *cdp, crashed <-chan struct{}) {
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	failures := 0
	for {
		select {
		case <-conn.Done():
			return
		case <-ctx.Done():
			return
		case <-crashed:
			b.log.Warn("page crashed, reloading")
			b.setErr("page crashed")
			b.mu.Lock()
			page := b.page
			b.mu.Unlock()
			if page.URL != "" {
				_ = b.apply(ctx, conn, page)
			}
		case <-tick.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Call(pctx, "Runtime.evaluate", map[string]any{"expression": "1", "returnByValue": true}, nil)
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

// Show navigates to page and remembers it so it is restored after a crash.
func (b *Browser) Show(ctx context.Context, page Page) error {
	b.mu.Lock()
	b.page = page
	conn := b.conn
	b.mu.Unlock()
	if conn == nil {
		return nil // applied when the browser connects
	}
	return b.apply(ctx, conn, page)
}

func (b *Browser) apply(ctx context.Context, conn *cdp, page Page) error {
	b.mu.Lock()
	old := b.scripts
	b.scripts = nil
	b.mu.Unlock()
	for _, id := range old {
		_ = conn.Call(ctx, "Page.removeScriptToEvaluateOnNewDocument", map[string]any{"identifier": id}, nil)
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
	b.scripts = ids
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
	return nil
}

// continueRequest adds the page's headers to a paused request. Fetch only
// pauses requests to the page's origin, so credentials stay there.
func (b *Browser) continueRequest(ctx context.Context, conn *cdp, params json.RawMessage) {
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
	extra := b.page.Headers
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
	if err := conn.Call(ctx, "Fetch.continueRequest", map[string]any{"requestId": ev.RequestID, "headers": hdrs}, nil); err != nil {
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
	k, ok := keys[name]
	if !ok {
		return fmt.Errorf("unsupported key %q", name)
	}
	conn, err := b.connection()
	if err != nil {
		return err
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

func (b *Browser) connection() (*cdp, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn == nil {
		return nil, errors.New("browser not connected")
	}
	return b.conn, nil
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
