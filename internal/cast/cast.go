// Package cast runs UxPlay, an AirPlay receiver, so iPhones, iPads and Macs
// can mirror their screen or send video and audio to the kiosk. Everything
// stays on the local network.
package cast

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/XtremeOwnage/pikiosk/internal/display"
)

// BasePort is UxPlay's first TCP port; it also uses the next two, and the
// same numbers over UDP.
const BasePort = 7100

type Options struct {
	Name string
	// PIN asks clients for a 4-digit code shown on screen the first time
	// they connect.
	PIN bool
}

type Receiver struct {
	binary     string
	runtimeDir string
	log        *slog.Logger

	mu       sync.Mutex
	opts     Options
	enabled  bool
	proc     *os.Process
	running  bool
	active   bool
	lastErr  string
	changed  chan struct{}
	onChange func(active bool)
}

func New(binary, runtimeDir string, log *slog.Logger) *Receiver {
	if binary == "" {
		binary = "uxplay"
	}
	return &Receiver{binary: binary, runtimeDir: runtimeDir, log: log.With("component", "cast"), changed: make(chan struct{}, 1)}
}

// Available reports whether the UxPlay binary is installed.
func (r *Receiver) Available() bool {
	_, err := exec.LookPath(r.binary)
	return err == nil
}

// OnChange is called when a client starts or stops casting.
func (r *Receiver) OnChange(fn func(active bool)) {
	r.mu.Lock()
	r.onChange = fn
	r.mu.Unlock()
}

// Configure starts, restarts or stops the receiver.
func (r *Receiver) Configure(enabled bool, opts Options) {
	r.mu.Lock()
	same := r.enabled == enabled && r.opts == opts
	r.enabled, r.opts = enabled, opts
	p := r.proc
	r.mu.Unlock()
	if same {
		return
	}
	if p != nil {
		_ = syscall.Kill(-p.Pid, syscall.SIGTERM)
	}
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

type Status struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Running   bool   `json:"running"`
	Active    bool   `json:"active"`
	Name      string `json:"name,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

func (r *Receiver) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Status{Enabled: r.enabled, Available: r.Available(), Running: r.running, Active: r.active, Name: r.opts.Name, LastError: r.lastErr}
}

// Run supervises UxPlay and watches for client connections until ctx ends.
func (r *Receiver) Run(ctx context.Context) {
	go r.watch(ctx)
	backoff := time.Second
	for ctx.Err() == nil {
		r.mu.Lock()
		enabled, opts := r.enabled, r.opts
		r.mu.Unlock()
		if !enabled {
			select {
			case <-ctx.Done():
				return
			case <-r.changed:
			}
			continue
		}
		start := time.Now()
		err := r.runOnce(ctx, opts)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			r.log.Warn("uxplay exited", "err", err)
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-r.changed:
		case <-time.After(backoff):
			backoff = min(backoff*2, time.Minute)
		}
	}
}

func (r *Receiver) runOnce(ctx context.Context, opts Options) error {
	wayland, ok := display.WaylandDisplay(r.runtimeDir)
	if !ok {
		return fmt.Errorf("display is not running")
	}
	args := []string{
		"-n", opts.Name, "-nh",
		"-p", strconv.Itoa(BasePort),
		"-fs",
		"-vs", "waylandsink",
		"-as", "pulsesink",
		"-reset", "15",
		"-nohold",
	}
	if opts.PIN {
		args = append(args, "-pin")
	}
	cmd := exec.Command(r.binary, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	cmd.Env = append(os.Environ(), "WAYLAND_DISPLAY="+wayland, "XDG_RUNTIME_DIR="+r.runtimeDir)
	out, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	cmd.Stdout = cmd.Stderr
	if err := cmd.Start(); err != nil {
		r.setErr(err.Error())
		return err
	}
	r.mu.Lock()
	r.proc, r.running, r.lastErr = cmd.Process, true, ""
	r.mu.Unlock()
	r.log.Info("airplay receiver started", "name", opts.Name)

	var last string
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			last = line
			r.log.Debug("uxplay", "line", line)
		}
	}
	err = cmd.Wait()
	r.mu.Lock()
	r.proc, r.running = nil, false
	if err != nil && ctx.Err() == nil && r.enabled {
		r.lastErr = last
	}
	r.mu.Unlock()
	return err
}

func (r *Receiver) setErr(s string) {
	r.mu.Lock()
	r.lastErr = s
	r.mu.Unlock()
}

// watch polls for an established AirPlay connection.
func (r *Receiver) watch(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		r.mu.Lock()
		running := r.running
		r.mu.Unlock()
		active := running && connected(BasePort, BasePort+1, BasePort+2)
		r.mu.Lock()
		changed := active != r.active
		r.active = active
		fn := r.onChange
		r.mu.Unlock()
		if changed {
			r.log.Info("airplay", "active", active)
			if fn != nil {
				fn(active)
			}
		}
	}
}

// connected reports whether a TCP connection to one of the local ports is
// established, from /proc/net/tcp and tcp6. UxPlay's main AirPlay port is
// the second of its three.
func connected(ports ...int) bool {
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, p := range ports {
			if establishedOn(string(data), fmt.Sprintf(":%04X", p)) {
				return true
			}
		}
	}
	return false
}

func establishedOn(table, hexPort string) bool {
	for _, line := range strings.Split(table, "\n")[1:] {
		fields := strings.Fields(line)
		// sl local_address rem_address st ...; 01 is ESTABLISHED.
		if len(fields) > 3 && strings.HasSuffix(fields[1], hexPort) && fields[3] == "01" {
			return true
		}
	}
	return false
}
