// Package player shows RTSP and other video streams full screen with mpv,
// on top of the browser.
package player

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/XtremeOwnage/pikiosk/internal/display"
)

type Player struct {
	binary     string
	runtimeDir string
	log        *slog.Logger

	mu      sync.Mutex
	out     *tail
	cmd     *exec.Cmd
	url     string
	gen     int
	onEnd   func(url string, err error)
	lastErr string
}

func New(binary, runtimeDir string, log *slog.Logger) *Player {
	if binary == "" {
		binary = "mpv"
	}
	return &Player{binary: binary, runtimeDir: runtimeDir, log: log.With("component", "player")}
}

// OnEnd is called when a stream stops on its own (not via Stop or Play).
func (p *Player) OnEnd(fn func(url string, err error)) {
	p.mu.Lock()
	p.onEnd = fn
	p.mu.Unlock()
}

func (p *Player) Play(url string) error {
	wayland, ok := display.WaylandDisplay(p.runtimeDir)
	if !ok {
		return errors.New("display is not running")
	}
	p.Stop()
	args := []string{
		"--fs",
		"--no-input-terminal",
		"--msg-level=all=warn",
		"--term-osd=no",
		"--no-osc",
		"--no-input-default-bindings",
		"--input-cursor=no",
		"--cursor-autohide=always",
		"--vo=gpu,wlshm",
		"--gpu-context=wayland",
		"--hwdec=auto-safe",
		"--profile=low-latency",
		"--untimed",
		"--cache=no",
		"--rtsp-transport=tcp",
		"--network-timeout=10",
		"--keep-open=no",
		"--title=pikiosk-stream",
		"--wayland-app-id=pikiosk-stream",
		"--mute=yes",
		"--", url,
	}
	cmd := exec.Command(p.binary, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	cmd.Env = append(os.Environ(), "WAYLAND_DISPLAY="+wayland, "XDG_RUNTIME_DIR="+p.runtimeDir)
	out := &tail{max: 20}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		return err
	}
	p.mu.Lock()
	p.gen++
	gen := p.gen
	p.cmd = cmd
	p.out = out
	p.url = url
	p.lastErr = ""
	p.mu.Unlock()
	p.log.Info("stream started", "url", redact(url))

	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		if p.gen != gen {
			p.mu.Unlock()
			return // replaced or stopped
		}
		p.cmd = nil
		p.url = ""
		msg := out.String()
		if err != nil {
			p.lastErr = err.Error()
			if msg != "" {
				p.lastErr += ": " + msg
			}
		}
		fn := p.onEnd
		p.mu.Unlock()
		p.log.Info("stream ended", "err", err, "output", msg)
		if fn != nil {
			fn(url, err)
		}
	}()
	return nil
}

func (p *Player) Stop() {
	p.mu.Lock()
	cmd := p.cmd
	out := p.out
	p.gen++
	p.cmd = nil
	p.out = nil
	p.url = ""
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	if msg := out.String(); msg != "" {
		p.log.Info("stream stopped", "output", msg)
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	go func() {
		time.Sleep(3 * time.Second)
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}()
}

func (p *Player) Playing() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.url, p.cmd != nil
}

func (p *Player) LastError() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastErr
}

// tail keeps the last lines written to it.
type tail struct {
	mu    sync.Mutex
	max   int
	lines []string
	part  []byte
}

func (t *tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.part = append(t.part, b...)
	for {
		i := bytes.IndexByte(t.part, '\n')
		if i < 0 {
			break
		}
		if line := strings.TrimSpace(string(t.part[:i])); line != "" {
			t.lines = append(t.lines, line)
			if len(t.lines) > t.max {
				t.lines = t.lines[1:]
			}
		}
		t.part = t.part[i+1:]
	}
	return len(b), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := t.lines
	if p := strings.TrimSpace(string(t.part)); p != "" {
		lines = append(lines, p)
	}
	return strings.Join(lines, "; ")
}

// Run stops the player when ctx ends.
func (p *Player) Run(ctx context.Context) {
	<-ctx.Done()
	p.Stop()
}

// redact hides credentials embedded in a stream URL.
func redact(u string) string {
	for i := 0; i+3 <= len(u); i++ {
		if u[i:i+3] == "://" {
			rest := u[i+3:]
			for j := 0; j < len(rest); j++ {
				switch rest[j] {
				case '/':
					return u
				case '@':
					return u[:i+3] + "***@" + rest[j+1:]
				}
			}
			return u
		}
	}
	return u
}

func Redact(u string) string { return redact(u) }
