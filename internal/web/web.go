// Package web serves the web client, built into dist by `just web` and embedded in the binary.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// shell is the page SvelteKit writes for every route it didn't prerender. The client router takes
// over from it.
const shell = "200.html"

const notBuilt = `<!doctype html><title>whatnow</title><p>UI not built, run <code>just build</code>.</p>`

// Handler serves the embedded client.
func Handler() http.Handler {
	files, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is embedded at build time, so it is always there
	}
	return handler(files)
}

// handler serves the file a path names, and the app shell for any other path, so a client route
// such as /settings loads the app. A path under /api/ is never the app: an unknown API route is a
// 404.
func handler(files fs.FS) http.Handler {
	static := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if info, err := fs.Stat(files, name); err == nil && !info.IsDir() {
			static.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		page, err := fs.ReadFile(files, shell)
		if err != nil {
			page = []byte(notBuilt)
		}
		w.Write(page)
	})
}
