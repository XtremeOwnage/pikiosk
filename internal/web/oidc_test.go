package web

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/XtremeOwnage/pikiosk/internal/config"
)

// fakeProvider is a minimal OpenID provider that signs in one user.
type fakeProvider struct {
	*httptest.Server
	key    *rsa.PrivateKey
	groups []string
	nonce  string
	// challenge seen on /authorize, checked against the verifier on /token.
	challenge string
}

func newFakeProvider(t *testing.T) *fakeProvider {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{key: key, groups: []string{"kiosk-admins"}}
	mux := http.NewServeMux()
	p.Server = httptest.NewServer(mux)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                p.URL,
			"authorization_endpoint":                p.URL + "/authorize",
			"token_endpoint":                        p.URL + "/token",
			"jwks_uri":                              p.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" {
			http.Error(w, "pkce required", 400)
			return
		}
		p.nonce, p.challenge = q.Get("nonce"), q.Get("code_challenge")
		http.Redirect(w, r, q.Get("redirect_uri")+"?code=c1&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("code") != "c1" || r.Form.Get("code_verifier") == "" {
			http.Error(w, `{"error":"invalid_grant"}`, 400)
			return
		}
		sig, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
		claims := map[string]any{
			"iss": p.URL, "sub": "u1", "aud": "pikiosk", "nonce": p.nonce,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(),
			"preferred_username": "eve", "groups": p.groups,
		}
		raw, _ := jwt.Signed(sig).Claims(claims).Serialize()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": raw, "expires_in": 60})
	})
	t.Cleanup(p.Close)
	return p
}

func newTestServer(t *testing.T, o config.OIDC) (*Server, *httptest.Server) {
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"), "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *config.Config) error { c.OIDC = o; return nil }); err != nil {
		t.Fatal(err)
	}
	s := &Server{Config: store, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func signIn(t *testing.T, ts *httptest.Server) (final *url.URL, auth map[string]any) {
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	res, err := c.Get(ts.URL + "/api/oidc/login")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	res, err = c.Get(ts.URL + "/api/auth")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	_ = json.NewDecoder(res.Body).Decode(&auth)
	return res.Request.URL, auth
}

func TestOIDCSignIn(t *testing.T) {
	p := newFakeProvider(t)
	_, ts := newTestServer(t, config.OIDC{Enabled: true, Issuer: p.URL, ClientID: "pikiosk", AllowedGroups: []string{"Kiosk-Admins"}})
	_, auth := signIn(t, ts)
	if auth["authenticated"] != true || auth["user"] != "eve" {
		t.Fatalf("auth = %v", auth)
	}
}

func TestOIDCGroupDenied(t *testing.T) {
	p := newFakeProvider(t)
	p.groups = []string{"others"}
	_, ts := newTestServer(t, config.OIDC{Enabled: true, Issuer: p.URL, ClientID: "pikiosk", AllowedGroups: []string{"kiosk-admins"}})
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	res, err := c.Get(ts.URL + "/api/oidc/login")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if e := res.Request.URL.Query().Get("login_error"); !strings.Contains(e, "not in an allowed group") {
		t.Fatalf("landed on %s", res.Request.URL)
	}
	res, _ = c.Get(ts.URL + "/api/auth")
	var auth map[string]any
	_ = json.NewDecoder(res.Body).Decode(&auth)
	res.Body.Close()
	if auth["authenticated"] == true {
		t.Fatal("signed in despite group check")
	}
}

func TestOIDCStateMismatch(t *testing.T) {
	p := newFakeProvider(t)
	_, ts := newTestServer(t, config.OIDC{Enabled: true, Issuer: p.URL, ClientID: "pikiosk"})
	c := &http.Client{} // no cookie jar: the state cookie never comes back
	res, err := c.Get(ts.URL + "/api/oidc/login")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if e := res.Request.URL.Query().Get("login_error"); !strings.Contains(e, "expired") {
		t.Fatalf("landed on %s", res.Request.URL)
	}
}

func TestStaticRevalidates(t *testing.T) {
	_, ts := newTestServer(t, config.OIDC{})
	res, err := http.Get(ts.URL + "/app.js")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	tag := res.Header.Get("ETag")
	if res.Header.Get("Cache-Control") != "no-cache" || tag == "" {
		t.Fatalf("headers %v", res.Header)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/app.js", nil)
	req.Header.Set("If-None-Match", tag)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotModified {
		t.Fatalf("status %d", res.StatusCode)
	}
}
