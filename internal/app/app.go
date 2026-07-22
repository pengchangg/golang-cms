package app

import (
	"io/fs"
	"mime"
	"net/http"
	"net/netip"
	"path"
	"strings"

	"cms/internal/platform/httpx"
)

type Module interface {
	RegisterRoutes(*http.ServeMux)
}

type handlerModule struct {
	handler  http.Handler
	patterns []string
}

func HandlerModule(handler http.Handler, patterns ...string) Module {
	return handlerModule{handler: handler, patterns: patterns}
}

func (m handlerModule) RegisterRoutes(mux *http.ServeMux) {
	for _, pattern := range m.patterns {
		mux.Handle(pattern, m.handler)
	}
}

func New(web fs.FS, trustedProxies []netip.Prefix, modules ...Module) http.Handler {
	mux := http.NewServeMux()
	for _, module := range modules {
		module.RegisterRoutes(mux)
	}
	mux.Handle("/", spaHandler(web))
	return httpx.SecurityHeaders(httpx.RequestID(httpx.ClientIP(trustedProxies, httpx.Recover(mux))))
}

func spaHandler(web fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			httpx.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "." || name == "" {
			name = "index.html"
		}
		data, err := fs.ReadFile(web, name)
		if err != nil && path.Ext(name) == "" {
			name = "index.html"
			data, err = fs.ReadFile(web, name)
		}
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if name == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		_, _ = w.Write(data)
	})
}
