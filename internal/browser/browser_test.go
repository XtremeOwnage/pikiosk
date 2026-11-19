package browser

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeChrome answers the DevTools calls the browser package makes.
type fakeChrome struct {
	mu    sync.Mutex
	calls []string
	srv   *httptest.Server
}

func newFakeChrome(t *testing.T) (*fakeChrome, int) {
	f := &fakeChrome{}
	up := websocket.Upgrader{}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		ws := "ws://" + r.Host + "/devtools/page/1"
		_ = json.NewEncoder(w).Encode([]target{{Type: "page", URL: "about:blank", WebSocketDebuggerURL: ws}})
	})
	mux.HandleFunc("/devtools/page/1", func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			var req struct {
				ID     int64           `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if c.ReadJSON(&req) != nil {
				return
			}
			f.mu.Lock()
			f.calls = append(f.calls, req.Method+" "+string(req.Params))
			f.mu.Unlock()
			var result any = map[string]any{}
			switch req.Method {
			case "Page.addScriptToEvaluateOnNewDocument":
				result = map[string]any{"identifier": strconv.FormatInt(req.ID, 10)}
			case "Page.captureScreenshot":
				result = map[string]any{"data": "aGVsbG8="}
			case "Page.getLayoutMetrics":
				result = map[string]any{"cssVisualViewport": map[string]any{"clientWidth": 1920, "clientHeight": 1080}}
			}
			_ = c.WriteJSON(map[string]any{"method": "Page.frameNavigated", "params": map[string]any{}})
			_ = c.WriteJSON(map[string]any{"id": req.ID, "result": result})
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(f.srv.URL, "http://"))
	p, _ := strconv.Atoi(port)
	return f, p
}

func (f *fakeChrome) find(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func connected(t *testing.T, port int) (*Browser, context.Context) {
	b := New(Options{Port: port}, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go b.connectLoop(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for !b.Status().Connected {
		if time.Now().After(deadline) {
			t.Fatal("never connected")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return b, ctx
}

func TestShowInjectsTokenAndNavigates(t *testing.T) {
	f, port := newFakeChrome(t)
	b, ctx := connected(t, port)

	if err := b.Show(ctx, Page{URL: "https://ha.lan:8123/lovelace/0?kiosk", HAToken: "tok", Zoom: 1.25}); err != nil {
		t.Fatal(err)
	}
	scripts := f.find("Page.addScriptToEvaluateOnNewDocument")
	if len(scripts) != 2 {
		t.Fatalf("want 2 injected scripts, got %d", len(scripts))
	}
	if !strings.Contains(scripts[0], `https://ha.lan:8123`) || !strings.Contains(scripts[0], "hassTokens") {
		t.Errorf("token script wrong: %s", scripts[0])
	}
	if !strings.Contains(scripts[1], "1.25") {
		t.Errorf("zoom script wrong: %s", scripts[1])
	}
	if nav := f.find("Page.navigate"); len(nav) != 1 || !strings.Contains(nav[0], "lovelace") {
		t.Errorf("navigate calls: %v", nav)
	}

	// The next page removes the previous scripts.
	if err := b.Show(ctx, Page{URL: "https://example.lan/"}); err != nil {
		t.Fatal(err)
	}
	if rm := f.find("Page.removeScriptToEvaluateOnNewDocument"); len(rm) != 2 {
		t.Errorf("want 2 removals, got %d", len(rm))
	}
}

func TestInput(t *testing.T) {
	f, port := newFakeChrome(t)
	b, ctx := connected(t, port)

	img, err := b.Screenshot(ctx, 50)
	if err != nil || string(img) != "hello" {
		t.Fatalf("screenshot %q %v", img, err)
	}
	if err := b.Click(ctx, 0.5, 0.25); err != nil {
		t.Fatal(err)
	}
	clicks := f.find("Input.dispatchMouseEvent")
	if len(clicks) != 2 || !strings.Contains(clicks[0], `"x":960`) || !strings.Contains(clicks[0], `"y":270`) {
		t.Errorf("click events: %v", clicks)
	}
	if err := b.Click(ctx, 2, 0); err == nil {
		t.Error("out of range click accepted")
	}
	if err := b.Key(ctx, "Enter"); err != nil {
		t.Fatal(err)
	}
	if err := b.Key(ctx, "F13"); err == nil {
		t.Error("unknown key accepted")
	}
}

func TestOriginOf(t *testing.T) {
	cases := map[string]string{
		"https://ha.lan:8123/lovelace?x=1": "https://ha.lan:8123",
		"http://ha.lan":                    "http://ha.lan",
		"http://ha.lan#frag":               "http://ha.lan",
	}
	for in, want := range cases {
		if got, err := originOf(in); err != nil || got != want {
			t.Errorf("%q: got %q %v", in, got, err)
		}
	}
}
