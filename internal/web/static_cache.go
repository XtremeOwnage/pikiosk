package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"
)

// revalidate makes browsers check embedded files on every load, so an update
// is picked up without a hard refresh. Unchanged files answer 304 by ETag.
func revalidate(files fs.FS, next http.Handler) http.Handler {
	var (
		mu   sync.Mutex
		tags = map[string]string{}
	)
	etag := func(name string) string {
		mu.Lock()
		defer mu.Unlock()
		if t, ok := tags[name]; ok {
			return t
		}
		data, err := fs.ReadFile(files, name)
		if err != nil {
			return ""
		}
		sum := sha256.Sum256(data)
		t := `"` + hex.EncodeToString(sum[:8]) + `"`
		tags[name] = t
		return t
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		w.Header().Set("Cache-Control", "no-cache")
		if t := etag(name); t != "" {
			w.Header().Set("ETag", t)
		}
		next.ServeHTTP(w, r)
	})
}
