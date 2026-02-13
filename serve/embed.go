package serve

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed frontend/dist/*
var frontendFS embed.FS

// frontendHandler returns an http.Handler that serves the embedded SPA.
// It serves static files from the embedded filesystem and falls back
// to index.html for SPA routing.
func frontendHandler() http.Handler {
	dist, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		// If frontend hasn't been built yet, serve a placeholder.
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(placeholderHTML))
		})
	}

	fileServer := http.FileServer(http.FS(dist))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the static file directly.
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}

		// Hashed assets can be cached forever; index.html must revalidate.
		if strings.HasPrefix(path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}

		// Check if the file exists in the embedded FS.
		f, err := dist.Open(strings.TrimPrefix(path, "/"))
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// SPA fallback: serve index.html for all non-file routes.
		w.Header().Set("Cache-Control", "no-cache")
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

const placeholderHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Vega Dashboard</title>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body { font-family: system-ui, -apple-system, sans-serif; background: #0a0a0b; color: #e4e4e7; display: flex; align-items: center; justify-content: center; min-height: 100vh; }
    .container { text-align: center; max-width: 600px; padding: 2rem; }
    h1 { font-size: 2rem; margin-bottom: 0.5rem; background: linear-gradient(135deg, #818cf8, #a78bfa); -webkit-background-clip: text; -webkit-text-fill-color: transparent; }
    p { color: #a1a1aa; margin-bottom: 1.5rem; }
    .api-link { color: #818cf8; text-decoration: none; border: 1px solid #27272a; padding: 0.75rem 1.5rem; border-radius: 0.5rem; display: inline-block; transition: all 0.2s; }
    .api-link:hover { border-color: #818cf8; background: rgba(129, 140, 248, 0.1); }
    code { background: #18181b; padding: 0.2rem 0.4rem; border-radius: 0.25rem; font-size: 0.875rem; }
  </style>
</head>
<body>
  <div class="container">
    <h1>Vega Dashboard</h1>
    <p>The frontend has not been built yet. The REST API is available.</p>
    <p>Build the frontend: <code>cd serve/frontend && npm install && npm run build</code></p>
    <a class="api-link" href="/api/stats">View API Stats →</a>
  </div>
</body>
</html>
`
