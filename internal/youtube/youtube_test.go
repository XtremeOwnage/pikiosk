package youtube

import (
	"context"
	"strings"
	"testing"

	"github.com/XtremeOwnage/pikiosk/internal/browser"
)

// fakeTV models the TV app's screens and remote-control focus.
type fakeTV struct {
	screen string
	items  []string
	idx    int
}

var screens = map[string]struct {
	text  string
	items []string
}{
	"welcome":  {"Watch the latest videos | Get started", []string{"Get started"}},
	"choose":   {"Sign in with your phone | Sign in with your remote | Add a kid account | Watch as guest", []string{"Sign in with your phone", "Sign in with your remote", "Add a kid account", "Watch as guest"}},
	"home":     {"Make YouTube your own | Sign in | Search | You're signed out", []string{"Sign in"}},
	"menu":     {"Home | Shorts | Settings", []string{"Home", "Shorts", "Subscriptions", "Library", "Music", "Settings"}},
	"settings": {"Settings | Autoplay next video | Link with Wi-Fi | Link with TV code", []string{"Settings"}},
	"list":     {"Settings | Autoplay next video | Link with Wi-Fi | Link with TV code", []string{"Autoplay next video", "Restricted mode", "Link with Wi-Fi", "Link with TV code"}},
	"code":     {"Link with TV code | 123 456 789 012", []string{"Back"}},
}

func (f *fakeTV) set(s string) { f.screen, f.items, f.idx = s, screens[s].items, 0 }

func (f *fakeTV) EvalIn(_ context.Context, _ browser.Page, expr string) (string, error) {
	if strings.Contains(expr, "activeElement") {
		return f.items[f.idx], nil
	}
	return screens[f.screen].text, nil
}

func (f *fakeTV) KeyIn(_ context.Context, _ browser.Page, key string) error {
	cur := f.items[f.idx]
	switch key {
	case "ArrowDown":
		if f.idx < len(f.items)-1 {
			f.idx++
		}
	case "ArrowLeft":
		if f.screen == "home" {
			f.set("menu")
		}
	case "ArrowRight":
		if f.screen == "settings" {
			f.set("list")
		}
	case "Enter":
		switch {
		case f.screen == "welcome":
			f.set("choose")
		case cur == "Watch as guest":
			f.set("home")
		case cur == "Settings":
			f.set("list")
		case cur == "Link with TV code":
			f.set("code")
		}
	}
	return nil
}

func TestLinkCodeFromFirstRun(t *testing.T) {
	tv := &fakeTV{}
	tv.set("welcome")
	code, err := LinkCode(context.Background(), tv, browser.Page{})
	if err != nil {
		t.Fatal(err)
	}
	if code != "123 456 789 012" {
		t.Fatalf("code %q", code)
	}
}

func TestPassWelcomeSkipsHome(t *testing.T) {
	tv := &fakeTV{}
	tv.set("home")
	if err := PassWelcome(context.Background(), tv, browser.Page{}); err != nil || tv.screen != "home" {
		t.Fatalf("err %v screen %s", err, tv.screen)
	}
}

func TestPlaying(t *testing.T) {
	if !Playing("https://www.youtube.com/tv#/watch?v=x") || Playing("https://www.youtube.com/tv#/") {
		t.Fatal("Playing wrong")
	}
}
