package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jefflunt/kamal-watch"
	"github.com/jefflunt/kamal-watch/config"
	"github.com/jefflunt/kamal-watch/pkg/models"
	"github.com/jefflunt/kamal-watch/pkg/monitor"
	"github.com/jefflunt/kamal-watch/pkg/remote"
)

//go:embed templates/*
var templateFS embed.FS

//go:embed assets/styles.css
var stylesCSS []byte

type Server struct {
	cfg                  *config.Config
	monitor              *monitor.Monitor
	pool                 *remote.SSHPool
	tmpl                 *template.Template
	streamSemMu          sync.Mutex
	streamSems           map[string]chan struct{}
	maxConcurrentStreams int
	streamFunc           func(ctx context.Context, serverName, cmd string, out io.Writer) error
}

func NewServer(cfg *config.Config, mon *monitor.Monitor, pool *remote.SSHPool) (*Server, error) {
	funcMap := template.FuncMap{
		"formatTime": func(t *time.Time) string {
			if t == nil || t.IsZero() {
				return "Unknown"
			}
			return t.Local().Format("Jan 02, 2006 15:04:05 MST")
		},
		"timeAgo": func(t *time.Time) string {
			if t == nil || t.IsZero() {
				return "Never"
			}
			diff := time.Since(*t)
			if diff < time.Minute {
				return "just now"
			} else if diff < time.Hour {
				return fmt.Sprintf("%dm ago", int(diff.Minutes()))
			} else if diff < 24*time.Hour {
				return fmt.Sprintf("%dh ago", int(diff.Hours()))
			}
			return fmt.Sprintf("%dd ago", int(diff.Hours()/24))
		},
		"truncate": func(s string, l int) string {
			if len(s) > l {
				return s[:l] + "..."
			}
			return s
		},
		"shortHash": func(s string) string {
			if len(s) > 10 {
				return s[:10]
			}
			return s
		},
		"join": strings.Join,
	}

	tmpl, err := template.New("base").Funcs(funcMap).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}

	maxStreams := config.DefaultMaxConcurrentLogStreams
	if cfg != nil && cfg.MaxConcurrentLogStreams > 0 {
		maxStreams = cfg.MaxConcurrentLogStreams
	}

	return &Server{
		cfg:                  cfg,
		monitor:              mon,
		pool:                 pool,
		tmpl:                 tmpl,
		streamSems:           make(map[string]chan struct{}),
		maxConcurrentStreams: maxStreams,
	}, nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /up", s.handleUp)
	mux.HandleFunc("GET /assets/styles.css", s.handleStylesCSS)
	mux.HandleFunc("GET /favicon.svg", s.handleFavicon)
	mux.HandleFunc("GET /favicon.ico", s.handleFavicon)
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /partial/servers", s.handleServersPartial)
	mux.HandleFunc("GET /server/{name}", s.handleServerView)
	mux.HandleFunc("GET /server/{name}/app/{app}", s.handleAppDetail)
	mux.HandleFunc("GET /api/inventory", s.handleAPIInventory)
	mux.HandleFunc("GET /events", s.handleSSEUpdates)
	mux.HandleFunc("GET /logs/stream", s.handleLogStream)

	return s.securityHeadersMiddleware(s.hostValidationMiddleware(s.authMiddleware(mux)))
}

func (s *Server) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleStylesCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(stylesCSS)
}

func (s *Server) handleUp(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK\n"))
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg == nil || (s.cfg.Auth.Username == "" && s.cfg.Auth.Password == "") {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/up" {
			next.ServeHTTP(w, r)
			return
		}

		user, pass, ok := r.BasicAuth()
		userMatch := subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.Auth.Username)) == 1
		passMatch := subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.Auth.Password)) == 1
		if !ok || !userMatch || !passMatch {
			w.Header().Set("WWW-Authenticate", `Basic realm="Kamal Watch"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func isLoopbackListenAddr(listenAddr string) bool {
	if listenAddr == "" || strings.HasPrefix(listenAddr, ":") {
		return false
	}
	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		host = listenAddr
	}
	host = strings.Trim(host, "[]")
	return strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
}

func extractHostname(hostHeader string) string {
	if h, _, err := net.SplitHostPort(hostHeader); err == nil {
		return strings.Trim(h, "[]")
	}
	return strings.Trim(hostHeader, "[]")
}

func (s *Server) hostValidationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg == nil {
			next.ServeHTTP(w, r)
			return
		}

		var allowed []string
		if len(s.cfg.AllowedHosts) > 0 {
			allowed = s.cfg.AllowedHosts
		} else if isLoopbackListenAddr(s.cfg.ListenAddr) {
			allowed = []string{"localhost", "127.0.0.1", "::1"}
		}

		if len(allowed) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		reqHost := r.Host
		reqHostname := extractHostname(reqHost)
		for _, candidate := range allowed {
			candidateClean := strings.Trim(candidate, "[]")
			if strings.EqualFold(reqHost, candidate) || strings.EqualFold(reqHostname, candidateClean) {
				next.ServeHTTP(w, r)
				return
			}
		}

		http.Error(w, "Forbidden: invalid Host header", http.StatusForbidden)
	})
}

const faviconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32" fill="none">
  <rect width="32" height="32" rx="6" fill="#0c1017" stroke="#30363d" stroke-width="1.5"/>
  <path d="M11 8H7V24H11" stroke="#58a6ff" stroke-width="2.5" stroke-linecap="square" stroke-linejoin="miter"/>
  <path d="M21 8H25V24H21" stroke="#58a6ff" stroke-width="2.5" stroke-linecap="square" stroke-linejoin="miter"/>
  <line x1="10" y1="16" x2="13.5" y2="16" stroke="#2ea043" stroke-width="2"/>
  <line x1="18.5" y1="16" x2="22" y2="16" stroke="#2ea043" stroke-width="2"/>
  <circle cx="16" cy="16" r="2.75" fill="#7ee787"/>
</svg>`

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write([]byte(faviconSVG))
}

type DashboardData struct {
	Servers []*models.ServerInventory
	Now     time.Time
	Version string
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	data := DashboardData{
		Servers: s.monitor.GetAllInventories(),
		Now:     time.Now(),
		Version: kamalwatch.Version(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleServersPartial(w http.ResponseWriter, r *http.Request) {
	data := DashboardData{
		Servers: s.monitor.GetAllInventories(),
		Now:     time.Now(),
		Version: kamalwatch.Version(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "servers_partial.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleServerView(w http.ResponseWriter, r *http.Request) {
	serverName := r.PathValue("name")
	inv, ok := s.monitor.GetInventory(serverName)
	if !ok {
		http.Error(w, "Server not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "server.html", inv); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type AppDetailData struct {
	ServerName string
	App        *models.KamalApp
}

func (s *Server) handleAppDetail(w http.ResponseWriter, r *http.Request) {
	serverName := r.PathValue("name")
	appName := r.PathValue("app")

	inv, ok := s.monitor.GetInventory(serverName)
	if !ok {
		http.Error(w, "Server not found", http.StatusNotFound)
		return
	}

	app, ok := inv.Apps[appName]
	if !ok {
		http.Error(w, "App not found on server", http.StatusNotFound)
		return
	}

	data := AppDetailData{
		ServerName: serverName,
		App:        app,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "app.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleAPIInventory(w http.ResponseWriter, r *http.Request) {
	inventories := s.monitor.GetAllInventories()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(inventories)
}

func (s *Server) handleSSEUpdates(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := s.monitor.Subscribe()
	defer s.monitor.Unsubscribe(ch)

	// Send an initial ping
	fmt.Fprintf(w, "event: ping\ndata: connected\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			data, _ := json.Marshal(s.monitor.GetAllInventories())
			fmt.Fprintf(w, "event: update\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

var validContainerIdentifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,254}$`)

const (
	defaultLogTail = 100
	maxLogTail     = 10000
)

func (s *Server) acquireStreamSlot(serverName string) (func(), bool) {
	s.streamSemMu.Lock()
	sem, ok := s.streamSems[serverName]
	if !ok {
		limit := s.maxConcurrentStreams
		if limit <= 0 {
			limit = config.DefaultMaxConcurrentLogStreams
		}
		sem = make(chan struct{}, limit)
		s.streamSems[serverName] = sem
	}
	s.streamSemMu.Unlock()

	select {
	case sem <- struct{}{}:
		return func() { <-sem }, true
	default:
		return nil, false
	}
}

func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, "Invalid query parameters", http.StatusBadRequest)
		return
	}

	serverName := query.Get("server")
	container := query.Get("container")
	tailParam := query.Get("tail")
	follow := query.Get("follow") != "false"

	if serverName == "" || container == "" {
		http.Error(w, "Missing server or container parameter", http.StatusBadRequest)
		return
	}

	// Strictly allowlist valid Docker container names and hex IDs to prevent command or flag injection
	if !validContainerIdentifier.MatchString(container) {
		http.Error(w, "Invalid container parameter", http.StatusBadRequest)
		return
	}

	tailLines := defaultLogTail
	if tailParam != "" {
		parsedTail, err := strconv.Atoi(tailParam)
		if err != nil || parsedTail < 1 || parsedTail > maxLogTail {
			http.Error(w, "Invalid tail parameter", http.StatusBadRequest)
			return
		}
		tailLines = parsedTail
	}

	if s.pool == nil && s.streamFunc == nil {
		http.Error(w, "SSH pool unavailable", http.StatusServiceUnavailable)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	releaseSlot, acquired := s.acquireStreamSlot(serverName)
	if !acquired {
		http.Error(w, "Too many concurrent log streams for this server", http.StatusTooManyRequests)
		return
	}
	defer releaseSlot()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	cmd := fmt.Sprintf("docker logs --tail %d", tailLines)
	if follow {
		cmd += " -f"
	}
	cmd += " " + container

	lw := &logWriter{
		w:       w,
		flusher: flusher,
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	if s.streamFunc != nil {
		_ = s.streamFunc(ctx, serverName, cmd, lw)
		return
	}
	_ = s.pool.StreamCommand(ctx, serverName, cmd, lw)
}

type logWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func (lw *logWriter) Write(p []byte) (n int, err error) {
	lines := strings.Split(string(p), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		data, _ := json.Marshal(line)
		fmt.Fprintf(lw.w, "data: %s\n\n", data)
	}
	lw.flusher.Flush()
	return len(p), nil
}
