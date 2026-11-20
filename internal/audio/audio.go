// Package audio plays notification sounds and announcements with mpv and
// manages the local sound library.
package audio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Extensions accepted in the sound library.
var Extensions = []string{".mp3", ".ogg", ".oga", ".opus", ".wav", ".flac", ".m4a", ".aac"}

const MaxUpload = 20 << 20

type Sound struct {
	Name    string `json:"name"`
	Builtin bool   `json:"builtin"`
	Size    int64  `json:"size"`
}

type Player struct {
	mpv        string
	runtimeDir string
	userDir    string   // writable library
	builtin    []string // read-only libraries
	log        *slog.Logger

	mu      sync.Mutex
	cmd     *exec.Cmd
	current string
	gen     int
	onEnd   func(src string, err error)
}

func New(mpv, runtimeDir, userDir string, builtin []string, log *slog.Logger) *Player {
	if mpv == "" {
		mpv = "mpv"
	}
	_ = os.MkdirAll(userDir, 0o700)
	return &Player{mpv: mpv, runtimeDir: runtimeDir, userDir: userDir, builtin: builtin, log: log.With("component", "audio")}
}

func (p *Player) OnEnd(fn func(src string, err error)) {
	p.mu.Lock()
	p.onEnd = fn
	p.mu.Unlock()
}

// List returns the library; user sounds shadow built-in ones of the same name.
func (p *Player) List() []Sound {
	byName := map[string]Sound{}
	add := func(dir string, builtin bool) {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.IsDir() || !slices.Contains(Extensions, strings.ToLower(filepath.Ext(e.Name()))) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			byName[name] = Sound{Name: name, Builtin: builtin, Size: info.Size()}
		}
	}
	for _, d := range p.builtin {
		add(d, true)
	}
	add(p.userDir, false)
	out := make([]Sound, 0, len(byName))
	for _, s := range byName {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b Sound) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func (p *Player) Names() []string {
	var names []string
	for _, s := range p.List() {
		names = append(names, s.Name)
	}
	return names
}

// Resolve turns a sound reference into something mpv can open: an http(s)
// URL, a library sound name, or a path inside one of the libraries.
func (p *Player) Resolve(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("no sound given")
	}
	if u, err := url.Parse(ref); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		return ref, nil
	}
	if strings.Contains(ref, "://") {
		return "", errors.New("only http and https URLs can be played")
	}
	dirs := append([]string{p.userDir}, p.builtin...)
	if filepath.IsAbs(ref) {
		clean := filepath.Clean(ref)
		for _, d := range dirs {
			if rel, err := filepath.Rel(d, clean); err == nil && !strings.HasPrefix(rel, "..") {
				if _, err := os.Stat(clean); err == nil {
					return clean, nil
				}
			}
		}
		return "", fmt.Errorf("%s is not in the sound library", ref)
	}
	name := filepath.Base(ref)
	for _, d := range dirs {
		for _, ext := range append([]string{""}, Extensions...) {
			cand := filepath.Join(d, name+ext)
			if st, err := os.Stat(cand); err == nil && !st.IsDir() {
				return cand, nil
			}
		}
	}
	return "", fmt.Errorf("unknown sound %q", ref)
}

// Play starts a sound, replacing any sound already playing. Volume 0-100
// scales this sound only; a negative volume plays at 100.
func (p *Player) Play(ref string, volume int) (string, error) {
	src, err := p.Resolve(ref)
	if err != nil {
		return "", err
	}
	if volume < 0 || volume > 100 {
		volume = 100
	}
	p.Stop()
	cmd := exec.Command(p.mpv,
		"--no-video", "--no-config", "--no-input-terminal", "--term-osd=no",
		"--msg-level=all=error", "--ao=pipewire,pulse,alsa",
		"--network-timeout=15", "--volume="+strconv.Itoa(volume),
		"--", src)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+p.runtimeDir)
	var out strings.Builder
	cmd.Stderr = &limited{w: &out, n: 2000}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	p.mu.Lock()
	p.gen++
	gen := p.gen
	p.cmd = cmd
	p.current = src
	p.mu.Unlock()
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		if p.gen != gen {
			p.mu.Unlock()
			return
		}
		p.cmd = nil
		p.current = ""
		fn := p.onEnd
		p.mu.Unlock()
		if err != nil && strings.TrimSpace(out.String()) != "" {
			err = fmt.Errorf("%w: %s", err, strings.TrimSpace(out.String()))
		}
		if fn != nil {
			fn(src, err)
		}
	}()
	return src, nil
}

func (p *Player) Stop() {
	p.mu.Lock()
	cmd := p.cmd
	p.gen++
	p.cmd = nil
	p.current = ""
	p.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}

func (p *Player) Playing() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.current, p.cmd != nil
}

// SetVolume sets the system output volume through WirePlumber.
func (p *Player) SetVolume(ctx context.Context, percent int) error {
	if percent < 0 || percent > 100 {
		return errors.New("volume must be 0-100")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "wpctl", "set-volume", "@DEFAULT_AUDIO_SINK@", fmt.Sprintf("%d%%", percent))
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+p.runtimeDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("wpctl: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Save stores an uploaded sound in the user library.
func (p *Player) Save(filename string, r io.Reader) (Sound, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	if !slices.Contains(Extensions, ext) {
		return Sound{}, fmt.Errorf("unsupported file type %q", ext)
	}
	name := sanitize(strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)))
	if name == "" {
		return Sound{}, errors.New("invalid file name")
	}
	tmp, err := os.CreateTemp(p.userDir, ".upload-*")
	if err != nil {
		return Sound{}, err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, io.LimitReader(r, MaxUpload+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Sound{}, err
	}
	if n > MaxUpload {
		return Sound{}, fmt.Errorf("file is larger than %d MiB", MaxUpload>>20)
	}
	// Replace any existing sound with this name, whatever its extension.
	for _, e := range Extensions {
		_ = os.Remove(filepath.Join(p.userDir, name+e))
	}
	if err := os.Rename(tmp.Name(), filepath.Join(p.userDir, name+ext)); err != nil {
		return Sound{}, err
	}
	return Sound{Name: name, Size: n}, nil
}

// File is an uploaded sound as stored, for backups.
type File struct {
	Name string `json:"file"`
	Data []byte `json:"data"`
}

// UserFiles returns the uploaded sounds with their contents.
func (p *Player) UserFiles() ([]File, error) {
	entries, err := os.ReadDir(p.userDir)
	if err != nil {
		return nil, err
	}
	var out []File
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !slices.Contains(Extensions, strings.ToLower(filepath.Ext(e.Name()))) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(p.userDir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, File{Name: e.Name(), Data: data})
	}
	return out, nil
}

// Delete removes a user sound. Built-in sounds cannot be deleted.
func (p *Player) Delete(name string) error {
	name = sanitize(name)
	removed := false
	for _, e := range Extensions {
		if os.Remove(filepath.Join(p.userDir, name+e)) == nil {
			removed = true
		}
	}
	if !removed {
		return fmt.Errorf("no user sound named %q", name)
	}
	return nil
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '.':
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

type limited struct {
	w io.Writer
	n int
}

func (l *limited) Write(b []byte) (int, error) {
	if l.n > 0 {
		k := min(len(b), l.n)
		_, _ = l.w.Write(b[:k])
		l.n -= k
	}
	return len(b), nil
}
