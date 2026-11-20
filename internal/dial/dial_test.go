package dial

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type fakeTV struct {
	mu      sync.Mutex
	url     string
	running bool
}

func (f *fakeTV) Launch(_ context.Context, u string) error {
	f.mu.Lock()
	f.url, f.running = u, true
	f.mu.Unlock()
	return nil
}
func (f *fakeTV) Stop(context.Context) error {
	f.mu.Lock()
	f.running = false
	f.mu.Unlock()
	return nil
}
func (f *fakeTV) Running() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.running }

func TestDIALFlow(t *testing.T) {
	tv := &fakeTV{}
	s := New(56789, StableUUID("test"), tv, slog.New(slog.DiscardHandler))
	s.Configure(true, "Hallway")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/dd.xml")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if app := res.Header.Get("Application-URL"); !strings.HasSuffix(app, "/apps/") {
		t.Fatalf("Application-URL %q", app)
	}
	if !strings.Contains(string(body), "<friendlyName>Hallway</friendlyName>") {
		t.Fatalf("dd.xml: %s", body)
	}

	status := func() string {
		res, err := http.Get(srv.URL + "/apps/YouTube")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	if !strings.Contains(status(), "<state>stopped</state>") {
		t.Fatal("not stopped at start")
	}

	res, err = http.Post(srv.URL+"/apps/YouTube", "text/plain", strings.NewReader("pairingCode=abc-123&v=dQw4w9WgXcQ&theme=cl"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || !strings.HasSuffix(res.Header.Get("Location"), "/apps/YouTube/run") {
		t.Fatalf("launch: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if !strings.HasPrefix(tv.url, TVURL+"?") || !strings.Contains(tv.url, "pairingCode=abc-123") {
		t.Fatalf("launched %q", tv.url)
	}
	if !strings.Contains(status(), "<state>running</state>") {
		t.Fatal("not running after launch")
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/apps/YouTube/run", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || tv.Running() {
		t.Fatalf("stop: %d running=%v", res.StatusCode, tv.Running())
	}

	res, _ = http.Get(srv.URL + "/apps/Netflix")
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("other app: %d", res.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/apps/YouTube", strings.NewReader(""))
	req.Header.Set("Origin", "https://evil.example")
	res, _ = http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin: %d", res.StatusCode)
	}
}

func TestLaunchURLStaysOnYouTube(t *testing.T) {
	for _, body := range []string{"", "%zz", "x=https://evil.example/&pairingCode=1", "@evil.example/"} {
		u := LaunchURL(body)
		if !strings.HasPrefix(u, TVURL) || (len(u) > len(TVURL) && u[len(TVURL)] != '?') {
			t.Errorf("%q -> %q", body, u)
		}
	}
}

func TestSearchResponse(t *testing.T) {
	st, ok := searchTarget([]byte("M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 1\r\nST: urn:dial-multiscreen-org:service:dial:1\r\n\r\n"))
	if !ok || st != ServiceType {
		t.Fatal("dial search not answered")
	}
	if _, ok := searchTarget([]byte("M-SEARCH * HTTP/1.1\r\nST: urn:schemas-upnp-org:device:MediaRenderer:1\r\n\r\n")); ok {
		t.Fatal("answered a non-DIAL search")
	}
	s := New(56789, "u-1", &fakeTV{}, slog.New(slog.DiscardHandler))
	r := s.searchResponse(ServiceType, net.ParseIP("10.0.0.5"))
	if !strings.Contains(r, "LOCATION: http://10.0.0.5:56789/dd.xml\r\n") || !strings.Contains(r, "USN: uuid:u-1::"+ServiceType) {
		t.Fatalf("response:\n%s", r)
	}
}
