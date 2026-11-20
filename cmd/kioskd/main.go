// Command kioskd runs the kiosk: it supervises the browser and video
// player and serves the web UI, REST API and MQTT bridge.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/XtremeOwnage/pikiosk/internal/audio"
	"github.com/XtremeOwnage/pikiosk/internal/browser"
	"github.com/XtremeOwnage/pikiosk/internal/cast"
	"github.com/XtremeOwnage/pikiosk/internal/config"
	"github.com/XtremeOwnage/pikiosk/internal/dial"
	"github.com/XtremeOwnage/pikiosk/internal/display"
	"github.com/XtremeOwnage/pikiosk/internal/kiosk"
	"github.com/XtremeOwnage/pikiosk/internal/mqtt"
	"github.com/XtremeOwnage/pikiosk/internal/player"
	"github.com/XtremeOwnage/pikiosk/internal/system"
	"github.com/XtremeOwnage/pikiosk/internal/update"
	"github.com/XtremeOwnage/pikiosk/internal/web"
)

var version = "dev"

func main() {
	stateDir := flag.String("state-dir", envOr("PIKIOSK_STATE_DIR", "/var/lib/pikiosk"), "directory for config and browser profile")
	listen := flag.String("listen", envOr("PIKIOSK_LISTEN", ":80"), "web UI and API listen address")
	chromium := flag.String("chromium", envOr("PIKIOSK_CHROMIUM", "chromium"), "Chromium binary")
	mpv := flag.String("mpv", envOr("PIKIOSK_MPV", "mpv"), "mpv binary")
	devtools := flag.Int("devtools-port", 9222, "Chromium DevTools port on loopback")
	chromiumFlags := flag.String("chromium-flags", os.Getenv("PIKIOSK_CHROMIUM_FLAGS"), "extra Chromium flags, space separated")
	debug := flag.Bool("debug", false, "debug logging")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	if err := run(log, *stateDir, *listen, *chromium, *mpv, *devtools, strings.Fields(*chromiumFlags)); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, stateDir, listen, chromium, mpv string, devtools int, chromiumFlags []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Open(filepath.Join(stateDir, "config.json"), defaultDeviceID())
	if err != nil {
		return err
	}

	runtimeDir := envOr("XDG_RUNTIME_DIR", "/run/user/"+strconv.Itoa(os.Getuid()))

	br := browser.New(browser.Options{
		Binary:     chromium,
		DataDir:    filepath.Join(stateDir, "chromium"),
		Port:       devtools,
		ExtraFlags: chromiumFlags,
		RuntimeDir: runtimeDir,
	}, log)
	pl := player.New(mpv, runtimeDir, log)
	var builtinSounds []string
	if v := os.Getenv("PIKIOSK_SOUNDS"); v != "" {
		builtinSounds = filepath.SplitList(v)
	}
	au := audio.New(mpv, runtimeDir, filepath.Join(stateDir, "sounds"), builtinSounds, log)
	port := listenPort(listen)
	k := kiosk.New(cfg, br, pl, au, display.New(runtimeDir), "http://127.0.0.1:"+port+"/welcome.html", log)

	rx := cast.New(envOr("PIKIOSK_UXPLAY", "uxplay"), runtimeDir, log)
	rx.OnChange(func(active bool) { k.SetCasting(context.Background(), active) })
	applyCast := func(c config.Config) {
		name := c.Cast.Name
		if name == "" {
			name = c.DeviceName
		}
		rx.Configure(c.Cast.AirPlay && rx.Available(), cast.Options{Name: name, PIN: c.Cast.PIN})
	}
	applyCast(cfg.Get())
	cfg.Subscribe(func(_, c config.Config) { applyCast(c) })

	if u := os.Getenv("PIKIOSK_YOUTUBE_TV"); u != "" {
		dial.TVURL = u
	}
	yt := dial.New(dialPort, dial.StableUUID(cfg.Get().DeviceID), youtubeTV{k: k, cfg: cfg}, log)
	applyYouTube := func(c config.Config) {
		name := c.Cast.Name
		if name == "" {
			name = c.DeviceName
		}
		yt.Configure(c.Cast.YouTube, name)
		if !c.Cast.YouTube && k.WebCasting() == "youtube" {
			_ = k.StopWebCast(context.Background())
		}
	}
	applyYouTube(cfg.Get())
	cfg.Subscribe(func(_, c config.Config) { applyYouTube(c) })

	webURL := func() string {
		addrs := system.LocalAddresses()
		if len(addrs) == 0 {
			return ""
		}
		if port == "80" {
			return "http://" + addrs[0]
		}
		return "http://" + net.JoinHostPort(addrs[0], port)
	}
	stats := func() map[string]any {
		st := system.Stats()
		st["browser_restarts"] = br.Status().Restarts
		st["version"] = version
		return st
	}
	bridge := mqtt.New(cfg, k, au.Names, stats, version, webURL, log)

	// PIKIOSK_RELEASES is shared with self-update.sh; empty turns checks off.
	releases, ok := os.LookupEnv("PIKIOSK_RELEASES")
	if !ok {
		releases = "XtremeOwnage/pikiosk"
	}
	upd := update.New(os.Getenv("PIKIOSK_RELEASES_API"), releases, version, log)
	upd.SetEnabled(!cfg.Get().UpdateCheck.Disabled)
	cfg.Subscribe(func(_, c config.Config) { upd.SetEnabled(!c.UpdateCheck.Disabled) })
	bridge.SetUpdateSource(upd.Status)
	upd.OnChange(func(st update.Status) {
		bridge.PublishUpdate()
		if st.Available {
			k.Emit("update_available", st.Latest)
		}
	})

	srv := &web.Server{Config: cfg, Kiosk: k, Browser: br, Player: pl, Audio: au, Cast: rx, MQTT: bridge, Stats: stats, Version: version, Port: port, Log: log, Update: upd, YouTube: yt}
	httpSrv := &http.Server{
		Addr:              listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go br.Run(ctx)
	go pl.Run(ctx)
	go rx.Run(ctx)
	go yt.Run(ctx)
	go k.Run(ctx)
	go bridge.Run(ctx)
	go upd.Run(ctx)
	go autoUpdate(ctx, cfg, upd, k, filepath.Join(stateDir, "auto-update.last"), log)

	errc := make(chan error, 1)
	go func() {
		log.Info("web UI listening", "addr", listen, "version", version)
		if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	system.Notify("READY=1")
	go watchdog(ctx, k, port, log)

	select {
	case <-ctx.Done():
	case err := <-errc:
		return err
	}
	system.Notify("STOPPING=1")
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdown)
	time.Sleep(500 * time.Millisecond) // let the MQTT offline message go out
	return nil
}

// watchdog pings systemd while the kiosk state and the web server respond,
// so a hung kioskd is restarted.
func watchdog(ctx context.Context, k *kiosk.Kiosk, port string, log *slog.Logger) {
	interval := system.WatchdogInterval()
	if interval == 0 {
		return
	}
	client := &http.Client{Timeout: 5 * time.Second}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		stateOK := make(chan struct{})
		go func() { k.State(); close(stateOK) }()
		select {
		case <-stateOK:
		case <-time.After(5 * time.Second):
			log.Error("watchdog: kiosk state is stuck")
			continue
		}
		resp, err := client.Get("http://127.0.0.1:" + port + "/api/auth")
		if err != nil {
			log.Error("watchdog: web server not answering", "err", err)
			continue
		}
		resp.Body.Close()
		system.Notify("WATCHDOG=1")
	}
}

func defaultDeviceID() string {
	b, err := os.ReadFile("/etc/machine-id")
	id := strings.TrimSpace(string(b))
	if err != nil || len(id) < 6 {
		return "kiosk"
	}
	return "kiosk_" + id[:6]
}

func listenPort(addr string) string {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "80"
	}
	return p
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// dialPort is where phones reach the YouTube cast receiver.
const dialPort = 56790

// youtubeUserAgent makes YouTube serve its TV app, which phones pair with.
const youtubeUserAgent = "Mozilla/5.0 (Linux armeabi-v7a; Android 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/114.0.0.0 Safari/537.36 Cobalt/25.lts.30.1034943-gold (unlike Gecko) v8/8.8.278.17-jit gles Starboard/15, pikiosk_RaspberryPi_2024/1.0 (pikiosk, Kiosk, Wired)"

// youtubeTV runs YouTube's TV app as a web cast for the DIAL receiver.
type youtubeTV struct {
	k   *kiosk.Kiosk
	cfg *config.Store
}

func (y youtubeTV) Launch(ctx context.Context, url string) error {
	ua := y.cfg.Get().Cast.YouTubeUserAgent
	if ua == "" {
		ua = youtubeUserAgent
	}
	return y.k.StartWebCast(ctx, "youtube", url, ua, youtubePlaying)
}

func (y youtubeTV) Stop(ctx context.Context) error { return y.k.StopWebCast(ctx) }
func (y youtubeTV) Running() bool                  { return y.k.WebCasting() == "youtube" }

// youtubePlaying reports whether the TV app is on a video rather than its
// home screen, from its URL (https://www.youtube.com/tv#/watch?v=…).
func youtubePlaying(url string) bool {
	return strings.Contains(url, "/watch")
}

// autoUpdate runs System -> Update once a day inside the configured window.
// The last start is kept in lastFile so a restart inside the window does
// not run it twice.
func autoUpdate(ctx context.Context, cfg *config.Store, upd *update.Checker, k *kiosk.Kiosk, lastFile string, log *slog.Logger) {
	var last time.Time
	if b, err := os.ReadFile(lastFile); err == nil {
		last, _ = time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
	}
	upd.SetLastAuto(last)
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		u := cfg.Get().UpdateCheck
		now := system.Now()
		if !u.Auto || u.Disabled || !u.InWindow(now) || now.Sub(last) < 20*time.Hour {
			continue
		}
		if st, err := system.UnitInfo(ctx, system.UpgradeUnit); err == nil && (st.ActiveState == "active" || st.ActiveState == "activating") {
			continue
		}
		if err := system.StartUnit(ctx, system.UpgradeUnit); err != nil {
			log.Warn("automatic update", "err", err)
			continue
		}
		last = now
		upd.SetLastAuto(now)
		_ = os.WriteFile(lastFile, []byte(now.Format(time.RFC3339)+"\n"), 0o600)
		k.Emit("update", "automatic update started")
		log.Info("automatic update started")
	}
}
