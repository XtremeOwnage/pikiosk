package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/XtremeOwnage/pikiosk/internal/browser"
	"github.com/XtremeOwnage/pikiosk/internal/config"
	"github.com/XtremeOwnage/pikiosk/internal/dial"
	"github.com/XtremeOwnage/pikiosk/internal/kiosk"
	"github.com/XtremeOwnage/pikiosk/internal/youtube"
)

// dialPort is where phones reach the YouTube cast receiver.
const dialPort = 56790

// youtubeUserAgent makes YouTube serve its TV app, which phones pair with.
const youtubeUserAgent = "Mozilla/5.0 (Linux armeabi-v7a; Android 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/114.0.0.0 Safari/537.36 Cobalt/25.lts.30.1034943-gold (unlike Gecko) v8/8.8.278.17-jit gles Starboard/15, pikiosk_RaspberryPi_2024/1.0 (pikiosk, Kiosk, Wired)"

// linkIdle gives time to type the TV code into the phone.
const linkIdle = 10 * time.Minute

// youtubeTV runs YouTube's TV app: launched by phones over DIAL, opened to
// "Link with TV code" from the web UI, and optionally kept running in a
// background tab so linked phones can cast at any time.
type youtubeTV struct {
	k   *kiosk.Kiosk
	b   *browser.Browser
	cfg *config.Store
	log *slog.Logger
}

func (y *youtubeTV) cast(url string, idle time.Duration) kiosk.WebCast {
	c := y.cfg.Get().Cast
	ua := c.YouTubeUserAgent
	if ua == "" {
		ua = youtubeUserAgent
	}
	return kiosk.WebCast{Source: "youtube", URL: url, UserAgent: ua, Playing: youtube.Playing, Idle: idle, Keep: c.YouTubeKeepReady}
}

// Launch is the DIAL launch from a phone on the same network.
func (y *youtubeTV) Launch(ctx context.Context, url string) error {
	return y.k.StartWebCast(ctx, y.cast(url, 0))
}

func (y *youtubeTV) Stop(ctx context.Context) error { return y.k.StopWebCast(ctx) }
func (y *youtubeTV) Running() bool                  { return y.k.WebCasting() == "youtube" }

// Link shows the TV app's "Link with TV code" screen and returns the code.
// The cast stays up for linkIdle so there is time to enter it.
func (y *youtubeTV) Link(ctx context.Context) (string, error) {
	c := y.cast(dial.TVURL, linkIdle)
	if err := y.k.StartWebCast(ctx, c); err != nil {
		return "", err
	}
	code, err := youtube.LinkCode(ctx, y.b, c.Page())
	if err != nil {
		y.log.Warn("link with TV code", "err", err)
		return "", err
	}
	y.k.Emit("youtube_link", "TV code shown")
	return code, nil
}

// keepReady keeps the TV app loaded in the background while the setting is
// on, and brings it to the front when a linked phone starts playing.
func (y *youtubeTV) keepReady(ctx context.Context) {
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	var (
		loadedAt  time.Time
		welcomed  bool
		wasPlay   bool
		lastPage  browser.Page
		lastCheck time.Time
	)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		cc := y.cfg.Get().Cast
		page := y.cast(dial.TVURL, 0).Page()
		if !cc.YouTube || !cc.YouTubeKeepReady {
			if lastPage.URL != "" {
				y.b.Drop(ctx, lastPage)
				lastPage = browser.Page{}
			}
			continue
		}
		lastPage = page
		if y.k.WebCasting() == "youtube" {
			wasPlay = true // do not re-trigger when the cast hands the tab back
			continue
		}
		loaded, _ := y.b.Loaded(page)
		if !loaded {
			if time.Since(loadedAt) > 30*time.Second {
				loadedAt, welcomed = time.Now(), false
				if err := y.b.Preload(ctx, page); err != nil {
					y.log.Debug("keep ready", "err", err)
				}
			}
			continue
		}
		if !welcomed {
			wctx, cancel := context.WithTimeout(ctx, 45*time.Second)
			_ = youtube.PassWelcome(wctx, y.b, page)
			cancel()
			welcomed = true
		}
		if time.Since(lastCheck) < 2*time.Second {
			continue
		}
		lastCheck = time.Now()
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		href, err := y.b.EvalIn(cctx, page, "location.href")
		cancel()
		if err != nil {
			continue
		}
		playing := youtube.Playing(href)
		if playing && !wasPlay {
			y.log.Info("linked phone started casting")
			if err := y.k.StartWebCast(ctx, y.cast(dial.TVURL, 0)); err != nil {
				y.log.Warn("show cast", "err", err)
			}
		}
		if !playing && wasPlay {
			// Back on the app's home screen: ready for the next cast.
			wasPlay = false
		}
		if playing && wasPlay && y.k.WebCasting() == "" {
			// The cast ended while a video was still up: go home so it does
			// not keep playing in the background.
			hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			_, _ = y.b.EvalIn(hctx, page, `location.hash="#/"`)
			cancel()
		}
	}
}
