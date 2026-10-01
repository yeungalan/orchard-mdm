// Package web embeds the administration console and shared static assets.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed static
var static embed.FS

// Handler serves /static/* and the console shell for every other path.
func Handler() http.Handler {
	sub, _ := fs.Sub(static, "static")
	files := http.FileServer(http.FS(sub))
	index, _ := fs.ReadFile(sub, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/static/") {
			w.Header().Set("Cache-Control", "public, max-age=300")
			http.StripPrefix("/static", files).ServeHTTP(w, r)
			return
		}
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}
