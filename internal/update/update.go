// Package update checks GitHub for a newer pikiosk release. It only reports;
// installing stays with the System → Update action.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Interval between checks.
const Interval = 12 * time.Hour

type Status struct {
	Enabled   bool      `json:"enabled"`
	Repo      string    `json:"repo,omitempty"`
	Current   string    `json:"current"`
	Latest    string    `json:"latest,omitempty"`
	URL       string    `json:"url,omitempty"`
	Published time.Time `json:"published,omitempty"`
	Available bool      `json:"available"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
	Error     string    `json:"error,omitempty"`
	// LastAuto is when the automatic update last started.
	LastAuto time.Time `json:"last_auto,omitzero"`
}

// SetLastAuto records when the automatic update last started.
func (c *Checker) SetLastAuto(t time.Time) {
	c.mu.Lock()
	c.status.LastAuto = t
	c.mu.Unlock()
}

type Checker struct {
	api     string
	repo    string
	current string
	client  *http.Client
	log     *slog.Logger

	mu       sync.Mutex
	enabled  bool
	status   Status
	kick     chan struct{}
	onChange func(Status)
}

// New creates a checker for repo ("owner/name"). An empty repo turns it off.
// api is the GitHub API base URL.
func New(api, repo, current string, log *slog.Logger) *Checker {
	if api == "" {
		api = "https://api.github.com"
	}
	return &Checker{
		api: strings.TrimRight(api, "/"), repo: repo, current: current,
		client: &http.Client{Timeout: 30 * time.Second},
		log:    log.With("component", "update"),
		status: Status{Repo: repo, Current: current},
		kick:   make(chan struct{}, 1),
	}
}

func (c *Checker) OnChange(fn func(Status)) {
	c.mu.Lock()
	c.onChange = fn
	c.mu.Unlock()
}

// SetEnabled turns periodic checks on or off. Turning them on checks soon.
func (c *Checker) SetEnabled(on bool) {
	c.mu.Lock()
	was := c.enabled
	c.enabled = on && c.repo != ""
	c.status.Enabled = c.enabled
	if !c.enabled {
		c.status.Latest, c.status.URL, c.status.Available, c.status.Error = "", "", false, ""
		c.status.Published, c.status.CheckedAt = time.Time{}, time.Time{}
	}
	st, fn := c.status, c.onChange
	c.mu.Unlock()
	if c.enabled && !was {
		c.Kick()
	}
	if was != c.enabled && fn != nil {
		fn(st)
	}
}

// Kick asks for a check now.
func (c *Checker) Kick() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

func (c *Checker) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Run checks shortly after start and then every Interval while enabled.
func (c *Checker) Run(ctx context.Context) {
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-c.kick:
		}
		c.mu.Lock()
		on := c.enabled
		c.mu.Unlock()
		if on {
			_ = c.Check(ctx)
		}
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
		t.Reset(Interval)
	}
}

// Check asks GitHub for the latest release now.
func (c *Checker) Check(ctx context.Context) error {
	c.mu.Lock()
	if !c.enabled {
		c.mu.Unlock()
		return errors.New("update checks are turned off")
	}
	c.mu.Unlock()

	rel, err := c.latest(ctx)
	c.mu.Lock()
	c.status.CheckedAt = time.Now()
	if err != nil {
		c.status.Error = err.Error()
	} else {
		c.status.Error = ""
		c.status.Latest, c.status.URL, c.status.Published = rel.Tag, rel.URL, rel.Published
		c.status.Available = Newer(rel.Tag, c.current)
	}
	st, fn := c.status, c.onChange
	c.mu.Unlock()
	if err != nil {
		c.log.Debug("update check failed", "err", err)
	} else if st.Available {
		c.log.Info("update available", "current", c.current, "latest", st.Latest)
	}
	if fn != nil {
		fn(st)
	}
	return err
}

type release struct {
	Tag       string    `json:"tag_name"`
	URL       string    `json:"html_url"`
	Published time.Time `json:"published_at"`
}

func (c *Checker) latest(ctx context.Context) (release, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.api+"/repos/"+c.repo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "pikiosk")
	res, err := c.client.Do(req)
	if err != nil {
		return release{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return release{}, fmt.Errorf("GitHub answered %s", res.Status)
	}
	var r release
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return release{}, err
	}
	if r.Tag == "" {
		return release{}, errors.New("release has no tag")
	}
	return r, nil
}

// Newer reports whether release tag is a newer version than current.
// current may be a git describe string such as v0.1.0-2-gabc1234-dirty,
// which counts as its base version. A current version that is not a
// version number (a plain commit, "dev") is never offered an update.
func Newer(tag, current string) bool {
	t, ok := parse(tag)
	if !ok {
		return false
	}
	c, ok := parse(current)
	if !ok {
		return false
	}
	for i := range t {
		if t[i] != c[i] {
			return t[i] > c[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
