// Package webui embeds and serves the built A2UI browser frontend.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

// The built frontend is a generated artifact and is NOT committed (only a
// .gitkeep is, so this embed still compiles). On a fresh checkout `dist` holds
// just the .gitkeep until `cd web && yarn build` populates it.
//
//go:embed all:dist
var dist embed.FS

// notBuiltHTML is served when no real build is embedded, instead of a 404 or a
// directory listing.
const notBuiltHTML = `<!doctype html><html lang="ru"><head><meta charset="utf-8">` +
	`<title>A2UI — фронтенд не собран</title></head>` +
	`<body style="font-family:system-ui;max-width:640px;margin:64px auto;padding:0 24px;color:#222">` +
	`<h2>Фронтенд не собран</h2><p>Соберите его и перезапустите оркестратор:</p>` +
	`<pre style="background:#f0f1f3;padding:12px;border-radius:8px">cd web && yarn install && yarn build</pre>` +
	`</body></html>`

// Handler serves the embedded frontend. Unknown paths fall back to index.html so
// the single-page app can boot from any URL. When no build is embedded it serves
// a friendly "not built" page.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	_, statErr := fs.Stat(sub, "index.html")
	built := statErr == nil
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !built {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(notBuiltHTML))
			return
		}
		if _, err := fs.Stat(sub, trimLeadingSlash(r.URL.Path)); err != nil && r.URL.Path != "/" {
			r.URL.Path = "/" // SPA fallback
		}
		fileServer.ServeHTTP(w, r)
	})
}

func trimLeadingSlash(p string) string {
	if len(p) > 0 && p[0] == '/' {
		return p[1:]
	}
	return p
}
