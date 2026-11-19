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
	"github.com/XtremeOwnage/pikiosk/internal/display"
	"github.com/XtremeOwnage/pikiosk/internal/kiosk"
	"github.com/XtremeOwnage/pikiosk/internal/mqtt"
	"github.com/XtremeOwnage/pikiosk/internal/player"
	"github.com/XtremeOwnage/pikiosk/internal/system"
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

	srv := &web.Server{Config: cfg, Kiosk: k, Browser: br, Player: pl, Audio: au, Cast: rx, MQTT: bridge, Stats: stats, Version: version, Port: port, Log: log}
	httpSrv := &http.Server{
		Addr:              listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go br.Run(ctx)
	go pl.Run(ctx)
	go rx.Run(ctx)
	go k.Run(ctx)
	go bridge.Run(ctx)

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
