package update

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		tag, cur string
		want     bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.1.0-2-ga306e54-dirty", false},
		{"v0.1.1", "v0.1.0-2-ga306e54", true},
		{"v0.1.0", "v0.2.0", false},
		{"v1.0.0", "v0.9.9", true},
		{"v0.10.0", "v0.9.0", true},
		{"v0.2.0", "dev", false},
		{"v0.2.0", "a306e54", false},
		{"nightly", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.tag, c.cur); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.tag, c.cur, got)
		}
	}
}

func TestCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"tag_name":"v0.3.0","html_url":"https://example/rel","published_at":"2026-11-19T00:00:00Z"}`)
	}))
	defer srv.Close()
	c := New(srv.URL, "o/r", "v0.2.0", slog.New(slog.DiscardHandler))
	if err := c.Check(context.Background()); err == nil {
		t.Fatal("checked while off")
	}
	c.SetEnabled(true)
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := c.Status()
	if !st.Available || st.Latest != "v0.3.0" || st.URL != "https://example/rel" {
		t.Fatalf("status %+v", st)
	}
	c.SetEnabled(false)
	if st := c.Status(); st.Enabled || st.Latest != "" || st.Available {
		t.Fatalf("status after off %+v", st)
	}
}
