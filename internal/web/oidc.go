package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/XtremeOwnage/pikiosk/internal/config"
)

const (
	oidcCookie      = "pikiosk_oidc"
	oidcPendingTTL  = 10 * time.Minute
	oidcProviderTTL = time.Hour
)

type oidcPending struct {
	nonce, verifier, redirect string
	exp                       time.Time
}

type oidcState struct {
	mu      sync.Mutex
	pending map[string]oidcPending
	issuer  string
	cached  *oidc.Provider
	fetched time.Time
}

func oidcClient(ctx context.Context) context.Context {
	return oidc.ClientContext(ctx, &http.Client{Timeout: 10 * time.Second})
}

// provider returns the cached discovery document for issuer.
func (o *oidcState) provider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	o.mu.Lock()
	if o.cached != nil && o.issuer == issuer && time.Since(o.fetched) < oidcProviderTTL {
		p := o.cached
		o.mu.Unlock()
		return p, nil
	}
	o.mu.Unlock()
	p, err := oidc.NewProvider(oidcClient(ctx), issuer)
	if err != nil {
		return nil, fmt.Errorf("discovery for %s: %w", issuer, err)
	}
	o.mu.Lock()
	o.issuer, o.cached, o.fetched = issuer, p, time.Now()
	o.mu.Unlock()
	return p, nil
}

func (o *oidcState) put(state string, p oidcPending) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.pending == nil {
		o.pending = map[string]oidcPending{}
	}
	now := time.Now()
	for k, v := range o.pending {
		if now.After(v.exp) {
			delete(o.pending, k)
		}
	}
	o.pending[state] = p
}

func (o *oidcState) take(state string) (oidcPending, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	p, ok := o.pending[state]
	delete(o.pending, state)
	if !ok || time.Now().After(p.exp) {
		return oidcPending{}, false
	}
	return p, true
}

func redirectURL(cfg config.OIDC, r *http.Request) string {
	if cfg.RedirectURL != "" {
		return cfg.RedirectURL
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/api/oidc/callback"
}

func oauthConfig(cfg config.OIDC, p *oidc.Provider, redirect string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  redirect,
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
}

// loginFailed sends the browser back to the login page with a message.
func loginFailed(w http.ResponseWriter, r *http.Request, err error) {
	http.Redirect(w, r, "/?login_error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
}

func (s *Server) oidcLogin(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config.Get().OIDC
	if !cfg.Enabled {
		loginFailed(w, r, errors.New("single sign-on is not enabled"))
		return
	}
	p, err := s.oidc.provider(r.Context(), cfg.Issuer)
	if err != nil {
		s.Log.Warn("oidc", "err", err)
		loginFailed(w, r, errors.New("cannot reach the sign-in provider"))
		return
	}
	state, nonce, verifier := config.RandomToken(), config.RandomToken(), oauth2.GenerateVerifier()
	redirect := redirectURL(cfg, r)
	s.oidc.put(state, oidcPending{nonce: nonce, verifier: verifier, redirect: redirect, exp: time.Now().Add(oidcPendingTTL)})
	// Lax so the cookie comes back on the provider's redirect.
	http.SetCookie(w, &http.Cookie{
		Name: oidcCookie, Value: state, Path: "/api/oidc/", HttpOnly: true,
		Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode, MaxAge: int(oidcPendingTTL.Seconds()),
	})
	u := oauthConfig(cfg, p, redirect).AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	http.Redirect(w, r, u, http.StatusFound)
}

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Path: "/api/oidc/", MaxAge: -1})
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		msg := q.Get("error_description")
		if msg == "" {
			msg = e
		}
		loginFailed(w, r, fmt.Errorf("sign-in failed: %s", msg))
		return
	}
	c, err := r.Cookie(oidcCookie)
	state := q.Get("state")
	if err != nil || state == "" || c.Value != state {
		loginFailed(w, r, errors.New("sign-in expired, try again"))
		return
	}
	pend, ok := s.oidc.take(state)
	if !ok {
		loginFailed(w, r, errors.New("sign-in expired, try again"))
		return
	}
	cfg := s.Config.Get().OIDC
	if !cfg.Enabled {
		loginFailed(w, r, errors.New("single sign-on is not enabled"))
		return
	}
	user, err := s.oidcExchange(r.Context(), cfg, pend, q.Get("code"))
	if err != nil {
		s.Log.Warn("oidc sign-in", "err", err)
		loginFailed(w, r, err)
		return
	}
	s.Log.Info("oidc sign-in", "user", user)
	s.newSession(w, user)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

type oidcClaims struct {
	Subject           string   `json:"sub"`
	PreferredUsername string   `json:"preferred_username"`
	Email             string   `json:"email"`
	Name              string   `json:"name"`
	Groups            []string `json:"groups"`
}

func (s *Server) oidcExchange(ctx context.Context, cfg config.OIDC, pend oidcPending, code string) (string, error) {
	p, err := s.oidc.provider(ctx, cfg.Issuer)
	if err != nil {
		return "", errors.New("cannot reach the sign-in provider")
	}
	ctx = oidcClient(ctx)
	tok, err := oauthConfig(cfg, p, pend.redirect).Exchange(ctx, code, oauth2.VerifierOption(pend.verifier))
	if err != nil {
		return "", fmt.Errorf("code exchange failed: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return "", errors.New("provider returned no ID token")
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: cfg.ClientID}).Verify(ctx, raw)
	if err != nil {
		return "", fmt.Errorf("ID token rejected: %w", err)
	}
	if idt.Nonce != pend.nonce {
		return "", errors.New("ID token nonce mismatch")
	}
	var cl oidcClaims
	if err := idt.Claims(&cl); err != nil {
		return "", err
	}
	if len(cl.Groups) == 0 && len(cfg.AllowedGroups) > 0 {
		// Some providers put groups only in userinfo.
		if ui, err := p.UserInfo(ctx, oauth2.StaticTokenSource(tok)); err == nil {
			var more oidcClaims
			if ui.Claims(&more) == nil {
				cl.Groups = more.Groups
			}
		}
	}
	user := cmpOr(cl.PreferredUsername, cl.Email, cl.Name, cl.Subject)
	if len(cfg.AllowedGroups) > 0 && !slices.ContainsFunc(cl.Groups, func(g string) bool {
		return slices.ContainsFunc(cfg.AllowedGroups, func(a string) bool { return strings.EqualFold(a, g) })
	}) {
		return "", fmt.Errorf("%s is not in an allowed group", user)
	}
	return user, nil
}

func cmpOr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (s *Server) putOIDC(w http.ResponseWriter, r *http.Request) {
	var o config.OIDC
	if !decode(w, r, &o) {
		return
	}
	if o.ClientSecret == redacted {
		o.ClientSecret = s.Config.Get().OIDC.ClientSecret
	}
	if o.Enabled {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		if _, err := oidc.NewProvider(oidcClient(ctx), strings.TrimSpace(o.Issuer)); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("cannot read the provider's discovery document: %w", err))
			return
		}
	}
	s.update(w, func(c *config.Config) error { c.OIDC = o; return nil })
}
