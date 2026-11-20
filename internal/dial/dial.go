// Package dial is a DIAL receiver for the YouTube app, the protocol smart
// TVs use to appear in the YouTube cast menu on phones. Phones find it over
// SSDP; launching opens YouTube's TV web app with the phone's pairing code,
// and the phone then controls playback through YouTube itself.
package dial

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// ServiceType is the SSDP search target of DIAL receivers.
	ServiceType = "urn:dial-multiscreen-org:service:dial:1"
	// App is the only DIAL application served.
	App = "YouTube"

	ssdpAddr = "239.255.255.250:1900"
)

// TVURL is YouTube's TV web app; launch parameters are appended.
var TVURL = "https://www.youtube.com/tv"

// Handler starts and stops the app on the screen.
type Handler interface {
	Launch(ctx context.Context, url string) error
	Stop(ctx context.Context) error
	Running() bool
}

type Server struct {
	port    int
	uuid    string
	handler Handler
	log     *slog.Logger

	mu         sync.Mutex
	name       string
	enabled    bool
	running    bool
	lastSearch time.Time
	searchFrom string
	lastLaunch time.Time
	changed    chan struct{}
}

// Status tells whether phones are finding the receiver.
type Status struct {
	Enabled bool `json:"enabled"`
	Running bool `json:"running"`
	Port    int  `json:"port"`
	// LastSearch is the last time a phone (another host) searched for
	// DIAL receivers; zero means none has reached the kiosk.
	LastSearch time.Time `json:"last_search,omitzero"`
	SearchFrom string    `json:"search_from,omitempty"`
	LastLaunch time.Time `json:"last_launch,omitzero"`
}

func (s *Server) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{Enabled: s.enabled, Running: s.running, Port: s.port, LastSearch: s.lastSearch, SearchFrom: s.searchFrom, LastLaunch: s.lastLaunch}
}

func (s *Server) setRunning(on bool) {
	s.mu.Lock()
	s.running = on
	s.mu.Unlock()
}

// New creates a receiver whose HTTP side listens on port.
func New(port int, uuid string, h Handler, log *slog.Logger) *Server {
	if uuid == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		uuid = fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	}
	return &Server{port: port, uuid: uuid, handler: h, log: log.With("component", "dial"), changed: make(chan struct{}, 1)}
}

// Configure turns the receiver on or off and sets the name phones show.
func (s *Server) Configure(enabled bool, name string) {
	s.mu.Lock()
	same := s.enabled == enabled && s.name == name
	s.enabled, s.name = enabled, name
	s.mu.Unlock()
	if !same {
		select {
		case s.changed <- struct{}{}:
		default:
		}
	}
}

func (s *Server) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled
}

func (s *Server) friendlyName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.name
}

// Run serves SSDP and HTTP while enabled, until ctx ends.
func (s *Server) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if !s.Enabled() {
			select {
			case <-ctx.Done():
				return
			case <-s.changed:
			}
			continue
		}
		rctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := s.serve(rctx); err != nil && rctx.Err() == nil {
				s.log.Warn("dial receiver stopped", "err", err)
			}
		}()
		select {
		case <-ctx.Done():
		case <-s.changed:
		case <-done:
			select {
			case <-ctx.Done():
			case <-time.After(10 * time.Second):
			}
		}
		cancel()
		<-done
	}
}

func (s *Server) serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(s.port))
	if err != nil {
		return err
	}
	hs := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = hs.Serve(ln) }()
	defer hs.Close()

	group, _ := net.ResolveUDPAddr("udp4", ssdpAddr)
	conn, err := net.ListenMulticastUDP("udp4", nil, group)
	if err != nil {
		return fmt.Errorf("ssdp: %w", err)
	}
	defer conn.Close()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	// Listen on every network the kiosk is on (for example wired and a
	// second Wi-Fi network for phones), including ones that come up later.
	go func() {
		joined := map[int]bool{}
		for {
			s.joinAll(conn, group.IP, joined)
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
		}
	}()
	s.log.Info("dial receiver started", "port", s.port)
	s.setRunning(true)
	defer s.setRunning(false)
	buf := make([]byte, 2048)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		st, ok := searchTarget(buf[:n])
		if !ok {
			continue
		}
		local := localAddrFor(from.IP)
		if local == nil {
			continue
		}
		if !from.IP.Equal(local) && !from.IP.IsLoopback() {
			s.mu.Lock()
			first := s.lastSearch.IsZero() || s.searchFrom != from.IP.String()
			s.lastSearch, s.searchFrom = time.Now(), from.IP.String()
			s.mu.Unlock()
			if first {
				s.log.Info("dial search", "from", from.IP.String())
			}
		}
		// Reply from port 1900, as other SSDP devices do; some firewalls
		// drop replies from other ports.
		_, _ = conn.WriteToUDP([]byte(s.searchResponse(st, local)), from)
	}
}

// searchTarget returns the ST of an M-SEARCH that DIAL should answer.
func searchTarget(msg []byte) (string, bool) {
	lines := strings.Split(string(msg), "\r\n")
	if len(lines) == 0 || !strings.HasPrefix(strings.ToUpper(lines[0]), "M-SEARCH ") {
		return "", false
	}
	for _, l := range lines[1:] {
		k, v, ok := strings.Cut(l, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(k), "ST") {
			continue
		}
		v = strings.TrimSpace(v)
		if v == ServiceType || v == "ssdp:all" {
			return ServiceType, true
		}
	}
	return "", false
}

func (s *Server) searchResponse(st string, local net.IP) string {
	return "HTTP/1.1 200 OK\r\n" +
		"CACHE-CONTROL: max-age=1800\r\n" +
		"DATE: " + time.Now().UTC().Format(http.TimeFormat) + "\r\n" +
		"EXT:\r\n" +
		"LOCATION: http://" + net.JoinHostPort(local.String(), strconv.Itoa(s.port)) + "/dd.xml\r\n" +
		"SERVER: Linux/1.0 UPnP/1.1 pikiosk/1.0\r\n" +
		"ST: " + st + "\r\n" +
		"USN: uuid:" + s.uuid + "::" + st + "\r\n" +
		"BOOTID.UPNP.ORG: 1\r\n\r\n"
}

// localAddrFor picks the local IPv4 address on the same subnet as peer.
func localAddrFor(peer net.IP) net.IP {
	ifs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var fallback net.IP
	for _, a := range ifs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.To4() == nil || n.IP.IsLoopback() {
			continue
		}
		if n.Contains(peer) {
			return n.IP
		}
		if fallback == nil {
			fallback = n.IP
		}
	}
	return fallback
}

// Handler serves the DIAL REST side.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /dd.xml", s.deviceDescription)
	mux.HandleFunc("GET /apps/{app}", s.appStatus)
	mux.HandleFunc("POST /apps/{app}", s.launch)
	mux.HandleFunc("DELETE /apps/{app}/run", s.stop)
	return mux
}

// allowedOrigin blocks web pages on other sites from driving the receiver;
// apps send no Origin, or a package: or YouTube origin.
func allowedOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" || strings.HasPrefix(o, "package:") {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Scheme == "https" && (u.Host == "www.youtube.com" || u.Host == "youtube.com" || u.Host == "m.youtube.com")
}

func (s *Server) appURL(r *http.Request) string {
	return "http://" + r.Host + "/apps/"
}

func (s *Server) deviceDescription(w http.ResponseWriter, r *http.Request) {
	type device struct {
		DeviceType   string `xml:"deviceType"`
		FriendlyName string `xml:"friendlyName"`
		Manufacturer string `xml:"manufacturer"`
		ModelName    string `xml:"modelName"`
		UDN          string `xml:"UDN"`
	}
	type root struct {
		XMLName     xml.Name `xml:"urn:schemas-upnp-org:device-1-0 root"`
		SpecVersion struct {
			Major int `xml:"major"`
			Minor int `xml:"minor"`
		} `xml:"specVersion"`
		Device device `xml:"device"`
	}
	d := root{Device: device{
		DeviceType: "urn:dial-multiscreen-org:device:dial:1", FriendlyName: s.friendlyName(),
		Manufacturer: "pikiosk", ModelName: "pikiosk", UDN: "uuid:" + s.uuid,
	}}
	d.SpecVersion.Major = 1
	w.Header().Set("Application-URL", s.appURL(r))
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	_, _ = io.WriteString(w, xml.Header)
	_ = xml.NewEncoder(w).Encode(d)
}

func (s *Server) appStatus(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("app") != App {
		http.NotFound(w, r)
		return
	}
	if !allowedOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	state, link := "stopped", ""
	if s.handler.Running() {
		state, link = "running", `<link rel="run" href="run"/>`
	}
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<service xmlns="urn:dial-multiscreen-org:schemas:dial" dialVer="2.1">
<name>%s</name>
<options allowStop="true"/>
<state>%s</state>
%s
</service>
`, App, state, link)
}

// LaunchURL turns a DIAL launch body (form encoded: pairingCode, v, t, …)
// into a YouTube TV URL. Only YouTube's own address can come out of it.
func LaunchURL(body string) string {
	q, err := url.ParseQuery(strings.TrimSpace(body))
	if err != nil || len(q) == 0 {
		return TVURL
	}
	return TVURL + "?" + q.Encode()
}

func (s *Server) launch(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("app") != App {
		http.NotFound(w, r)
		return
	}
	if !allowedOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		http.Error(w, "launch data too large", http.StatusRequestEntityTooLarge)
		return
	}
	target := LaunchURL(string(body))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.handler.Launch(ctx, target); err != nil {
		s.log.Warn("launch", "err", err)
		http.Error(w, "launch failed", http.StatusServiceUnavailable)
		return
	}
	s.log.Info("youtube launched", "from", r.RemoteAddr, "pairing", strings.Contains(string(body), "pairingCode"))
	s.mu.Lock()
	s.lastLaunch = time.Now()
	s.mu.Unlock()
	w.Header().Set("Location", s.appURL(r)+App+"/run")
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("app") != App {
		http.NotFound(w, r)
		return
	}
	if !allowedOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !s.handler.Running() {
		http.NotFound(w, r)
		return
	}
	if err := s.handler.Stop(r.Context()); err != nil {
		http.Error(w, "stop failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// StableUUID derives a UUID from a device ID, so phones keep recognising
// the kiosk across restarts.
func StableUUID(id string) string {
	h := sha256.Sum256([]byte("pikiosk-dial:" + id))
	s := hex.EncodeToString(h[:16])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}

// joinAll joins the SSDP group on each up IPv4 multicast interface not in
// joined yet.
func (s *Server) joinAll(conn *net.UDPConn, group net.IP, joined map[int]bool) {
	ifs, err := net.Interfaces()
	if err != nil {
		return
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return
	}
	for _, ifi := range ifs {
		if joined[ifi.Index] || ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifi.Addrs()
		has4 := false
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				has4 = true
			}
		}
		if !has4 {
			continue
		}
		mreq := &syscall.IPMreqn{Ifindex: int32(ifi.Index)}
		copy(mreq.Multiaddr[:], group.To4())
		var serr error
		_ = raw.Control(func(fd uintptr) {
			serr = syscall.SetsockoptIPMreqn(int(fd), syscall.IPPROTO_IP, syscall.IP_ADD_MEMBERSHIP, mreq)
		})
		if serr == nil || errors.Is(serr, syscall.EADDRINUSE) {
			joined[ifi.Index] = true
			if serr == nil {
				s.log.Info("ssdp listening", "interface", ifi.Name)
			}
		}
	}
}
