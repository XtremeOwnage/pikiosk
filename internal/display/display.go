// Package display controls the screen through the sway compositor and,
// for brightness, DDC/CI.
package display

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Display struct {
	runtimeDir string
	swaymsg    string
	ddcutil    string
}

func New(runtimeDir string) *Display {
	return &Display{runtimeDir: runtimeDir, swaymsg: "swaymsg", ddcutil: "ddcutil"}
}

// WaylandDisplay returns the compositor's socket name in runtimeDir, such as
// "wayland-1", preferring $WAYLAND_DISPLAY when it exists.
func WaylandDisplay(runtimeDir string) (string, bool) {
	if name := os.Getenv("WAYLAND_DISPLAY"); name != "" {
		if _, err := os.Stat(filepath.Join(runtimeDir, name)); err == nil {
			return name, true
		}
	}
	matches, _ := filepath.Glob(filepath.Join(runtimeDir, "wayland-[0-9]*"))
	for _, m := range matches {
		if !strings.HasSuffix(m, ".lock") {
			return filepath.Base(m), true
		}
	}
	return "", false
}

func (d *Display) socket() (string, error) {
	if s := os.Getenv("SWAYSOCK"); s != "" {
		if _, err := os.Stat(s); err == nil {
			return s, nil
		}
	}
	matches, _ := filepath.Glob(filepath.Join(d.runtimeDir, "sway-ipc.*.sock"))
	if len(matches) == 0 {
		return "", errors.New("sway is not running")
	}
	// The newest socket belongs to the running compositor.
	newest, newestT := "", time.Time{}
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil && st.ModTime().After(newestT) {
			newest, newestT = m, st.ModTime()
		}
	}
	return newest, nil
}

func (d *Display) sway(ctx context.Context, cmd string) error {
	sock, err := d.socket()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, d.swaymsg, "-s", sock, "--", cmd).CombinedOutput()
	if err != nil {
		return fmt.Errorf("swaymsg %s: %v: %s", cmd, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (d *Display) SetPower(ctx context.Context, on bool) error {
	state := "off"
	if on {
		state = "on"
	}
	return d.sway(ctx, "output * power "+state)
}

func (d *Display) SetRotation(ctx context.Context, degrees int) error {
	t := "normal"
	if degrees != 0 {
		t = strconv.Itoa(degrees)
	}
	return d.sway(ctx, "output * transform "+t)
}

func (d *Display) SetScale(ctx context.Context, scale float64) error {
	return d.sway(ctx, "output * scale "+strconv.FormatFloat(scale, 'f', -1, 64))
}

func (d *Display) SetHideCursor(ctx context.Context, seconds int) error {
	if seconds <= 0 {
		return d.sway(ctx, "seat * hide_cursor 0")
	}
	return d.sway(ctx, fmt.Sprintf("seat * hide_cursor %d", seconds*1000))
}

// SetBrightness sets the monitor backlight over DDC/CI (VCP feature 0x10).
func (d *Display) SetBrightness(ctx context.Context, percent int) error {
	if percent < 0 || percent > 100 {
		return errors.New("brightness must be 0-100")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, d.ddcutil, "--noverify", "setvcp", "10", strconv.Itoa(percent)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ddcutil: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Ping checks that the compositor answers. It returns false with no error
// when the compositor is not running at all.
func (d *Display) Ping(ctx context.Context) (bool, error) {
	if _, err := d.socket(); err != nil {
		return false, nil
	}
	if err := d.swayQuery(ctx, "get_version"); err != nil {
		return true, err
	}
	return true, nil
}

func (d *Display) swayQuery(ctx context.Context, typ string) error {
	sock, err := d.socket()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, d.swaymsg, "-s", sock, "-t", typ).Run()
}

// RestartCompositor kills sway; greetd starts a fresh session.
func (d *Display) RestartCompositor() error {
	return exec.Command("pkill", "-KILL", "-u", strconv.Itoa(os.Getuid()), "-x", "sway").Run()
}
