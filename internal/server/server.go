// Package server wires the ASU compatible API and the OpenForge frontend into
// a single HTTP handler.
package server

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/openforge/openforge/internal/asu"
	"github.com/openforge/openforge/internal/builder"
	"github.com/openforge/openforge/internal/config"
	"github.com/openforge/openforge/internal/web"
)

// Server holds the HTTP dependencies.
type Server struct {
	cfg      *config.Config
	version  string
	logger   *slog.Logger
	handlers *asu.Handlers
	mux      *http.ServeMux
	local    *builder.LocalBackend
}

// New assembles a Server.
func New(cfg *config.Config, version string, logger *slog.Logger) (*Server, error) {
	meta := asu.NewMetadata(cfg, logger)

	var backend asu.Backend
	var local *builder.LocalBackend
	if strings.TrimSpace(cfg.BackendURL) != "" {
		backend = asu.NewRemoteBackend(cfg, "openforge/"+version)
	} else {
		builtin, err := builder.NewLocalBackend(cfg, meta, logger)
		if err != nil {
			logger.Error("failed to start built-in builder", "err", err)
			backend = asu.NewUnavailableBackend("built-in builder unavailable: " + err.Error())
		} else {
			backend = builtin
			local = builtin
		}
	}

	s := &Server{
		cfg:      cfg,
		version:  version,
		logger:   logger,
		handlers: asu.NewHandlers(cfg, meta, backend, version, logger),
		mux:      http.NewServeMux(),
		local:    local,
	}
	s.routes()
	return s, nil
}

// Close shuts down background workers.
func (s *Server) Close() {
	if s.local != nil {
		s.local.Close()
	}
}

func (s *Server) routes() {
	h := s.handlers
	m := s.mux

	// ASU build API.
	m.HandleFunc("POST /api/v1/build", h.BuildPost)
	m.HandleFunc("GET /api/v1/build/{hash}", h.BuildGet)
	m.HandleFunc("GET /api/v1/revision/{version}/{target}/{subtarget}", h.Revision)
	m.HandleFunc("GET /api/v1/latest", h.LatestRedirect)
	m.HandleFunc("GET /api/v1/overview", h.OverviewRedirect)

	// Statistics.
	m.HandleFunc("GET /api/v1/stats", h.Stats)
	m.HandleFunc("GET /api/v1/stats/summary", h.StatsSummary)
	m.HandleFunc("GET /api/v1/builds-per-day", h.ProxyStatsEndpoint("/builds-per-day", "builds-per-day"))
	m.HandleFunc("GET /api/v1/builds-by-version", h.ProxyStatsEndpoint("/builds-by-version", "builds-by-version"))
	m.HandleFunc("GET /api/v1/top-packages", h.ProxyStatsEndpoint("/top-packages", "top-packages"))
	m.HandleFunc("GET /api/v1/build-errors", h.ProxyStatsEndpoint("/build-errors", "build-errors"))

	// ASU metadata API.
	m.HandleFunc("GET /json/v1/latest.json", h.JSONLatest)
	m.HandleFunc("GET /json/v1/branches.json", h.JSONBranches)
	m.HandleFunc("GET /json/v1/overview.json", h.JSONOverview)
	m.HandleFunc("GET /json/v1/", h.JSONV1)

	// Artifact store.
	m.HandleFunc("GET /store/", h.Store)

	// Frontend.
	m.Handle("GET /static/", http.StripPrefix("/static/", web.Static()))
	m.HandleFunc("GET /favicon.ico", s.favicon)
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
	m.HandleFunc("GET /{$}", s.page("index.html", "/"))
	m.HandleFunc("GET /stats", s.page("stats.html", "/stats"))
}

func (s *Server) favicon(w http.ResponseWriter, r *http.Request) {
	data, err := web.ReadFile("favicon.svg")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}

func (s *Server) page(name, active string) http.HandlerFunc {
	tmpl, err := web.Templates()
	return func(w http.ResponseWriter, r *http.Request) {
		if err != nil {
			http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		data := map[string]any{
			"SiteName":          s.cfg.SiteName,
			"Version":           s.version,
			"Year":              time.Now().Year(),
			"ServerStats":       s.cfg.ServerStats,
			"AllowDefaults":     s.cfg.AllowDefaults,
			"MaxRootfsSizeMB":   s.cfg.MaxCustomRootfsSizeMB,
			"MaxDefaultsLength": s.cfg.MaxDefaultsLength,
			"Active":            active,
			"BackendConfigured": s.handlers.Backend.Endpoint() != "",
			"Contact":           s.cfg.Contact,
		}
		if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
			s.logger.Error("render template", "name", name, "err", err)
		}
	}
}

// Handler returns the composed HTTP handler with middleware applied.
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	h = s.securityHeaders(h)
	h = s.cors(h)
	h = s.logRequests(h)
	h = s.recoverPanic(h)
	return h
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		next.ServeHTTP(w, r)
	})
}

// cors exposes the read-only metadata API to browser clients such as the
// OpenWrt firmware selector hosted on another origin.
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/json/") {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, HEAD, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Expose-Headers", "X-Imagebuilder-Status, X-Queue-Position")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if strings.HasPrefix(r.URL.Path, "/static/") {
			return
		}
		s.logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start).Round(time.Millisecond).String(),
		)
	})
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("panic", "err", rec, "path", r.URL.Path)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
