// Package youtube drives YouTube's TV web app the way a remote would: past
// its welcome screen and to "Link with TV code", whose code pairs a phone
// with the kiosk through YouTube's servers, so no local discovery is needed.
package youtube

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/XtremeOwnage/pikiosk/internal/browser"
)

// Tab is the browser tab running the TV app.
type Tab interface {
	EvalIn(ctx context.Context, page browser.Page, expr string) (string, error)
	KeyIn(ctx context.Context, page browser.Page, key string) error
}

const (
	textJS  = `document.body ? document.body.innerText : ""`
	focusJS = `(()=>{const e=document.activeElement;return e?(e.getAttribute("aria-label")||e.innerText||"").replace(/\s+/g," ").trim():""})()`
)

// codeRE matches a TV code as the app shows it: 12 digits in groups of 3.
var codeRE = regexp.MustCompile(`\b\d{3}[ -]\d{3}[ -]\d{3}[ -]\d{3}\b`)

type driver struct {
	t    Tab
	page browser.Page
}

func (d driver) text(ctx context.Context) string {
	s, _ := d.t.EvalIn(ctx, d.page, textJS)
	return s
}

func (d driver) focus(ctx context.Context) string {
	s, _ := d.t.EvalIn(ctx, d.page, focusJS)
	return s
}

// waitText waits until the page text contains one of want.
func (d driver) waitText(ctx context.Context, timeout time.Duration, want ...string) (string, bool) {
	end := time.Now().Add(timeout)
	for {
		txt := d.text(ctx)
		for _, w := range want {
			if strings.Contains(txt, w) {
				return txt, true
			}
		}
		if time.Now().After(end) || ctx.Err() != nil {
			return txt, false
		}
		sleep(ctx, 500*time.Millisecond)
	}
}

// moveTo presses key until the focused item's label contains want.
func (d driver) moveTo(ctx context.Context, key, want string, max int) bool {
	for i := 0; i <= max; i++ {
		if strings.Contains(d.focus(ctx), want) {
			return true
		}
		if d.t.KeyIn(ctx, d.page, key) != nil {
			return false
		}
		sleep(ctx, 400*time.Millisecond)
	}
	return strings.Contains(d.focus(ctx), want)
}

func (d driver) press(ctx context.Context, key string) {
	_ = d.t.KeyIn(ctx, d.page, key)
	sleep(ctx, 400*time.Millisecond)
}

// PassWelcome picks "Watch as guest" on the first-run screen, if shown, so
// the app reaches its home screen and stays online for linked phones.
func PassWelcome(ctx context.Context, t Tab, page browser.Page) error {
	d := driver{t, page}
	txt, _ := d.waitText(ctx, 20*time.Second, "Get started", "Watch as guest", "Sign in", "Home")
	if strings.Contains(txt, "Get started") && !strings.Contains(txt, "Watch as guest") {
		d.press(ctx, "Enter")
		txt, _ = d.waitText(ctx, 10*time.Second, "Watch as guest")
	}
	if !strings.Contains(txt, "Watch as guest") {
		return nil
	}
	if !d.moveTo(ctx, "ArrowDown", "Watch as guest", 8) {
		return errors.New("could not find Watch as guest")
	}
	d.press(ctx, "Enter")
	d.waitText(ctx, 10*time.Second, "signed out", "Sign in", "Search")
	return nil
}

// LinkCode opens Settings → Link with TV code and returns the code shown.
func LinkCode(ctx context.Context, t Tab, page browser.Page) (string, error) {
	d := driver{t, page}
	if err := PassWelcome(ctx, t, page); err != nil {
		return "", err
	}
	if c := codeRE.FindString(d.text(ctx)); c != "" {
		return c, nil
	}
	// The side menu opens to the left; Settings is near its bottom.
	d.press(ctx, "Escape")
	d.press(ctx, "ArrowLeft")
	if !d.moveTo(ctx, "ArrowDown", "Settings", 30) {
		return "", errors.New("could not find Settings in the TV app's menu")
	}
	d.press(ctx, "Enter")
	if _, ok := d.waitText(ctx, 10*time.Second, "Link with TV code"); !ok {
		return "", errors.New("the TV app's settings did not open")
	}
	// Focus starts in the settings list; if it landed in the detail pane
	// on the right, step back into the list.
	if !d.moveTo(ctx, "ArrowDown", "Link with TV code", 20) {
		d.press(ctx, "ArrowLeft")
		if !d.moveTo(ctx, "ArrowDown", "Link with TV code", 20) {
			return "", errors.New("could not find Link with TV code")
		}
	}
	d.press(ctx, "Enter")
	end := time.Now().Add(15 * time.Second)
	for time.Now().Before(end) && ctx.Err() == nil {
		if c := codeRE.FindString(d.text(ctx)); c != "" {
			return c, nil
		}
		sleep(ctx, 500*time.Millisecond)
	}
	return "", fmt.Errorf("no TV code appeared")
}

// Playing reports whether the TV app is on a video (…/tv#/watch?v=…).
func Playing(url string) bool {
	return strings.Contains(url, "/watch")
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}
