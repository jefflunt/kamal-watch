package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jefflunt/kamal-watch"
	"github.com/jefflunt/kamal-watch/config"
	"github.com/jefflunt/kamal-watch/pkg/monitor"
)

func TestDashboardShowsVersionInsteadOfLive(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Servers = nil
	mon := monitor.NewMonitor(cfg, nil)

	srv, err := NewServer(cfg, mon, nil)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for /, got %d", w.Code)
	}

	body := w.Body.String()
	wantVersion := "v" + kamalwatch.Version()
	if !strings.Contains(body, wantVersion) {
		t.Errorf("expected dashboard header to contain %q", wantVersion)
	}
	if strings.Contains(body, ">LIVE<") {
		t.Errorf("expected LIVE label to be removed from dashboard header")
	}
}

func TestFaviconEndpoints(t *testing.T) {
	srv, err := NewServer(config.DefaultConfig(), nil, nil)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	routes := srv.Routes()

	// Test /favicon.svg
	req := httptest.NewRequest(http.MethodGet, "/favicon.svg", nil)
	w := httptest.NewRecorder()
	routes.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for /favicon.svg, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("expected image/svg+xml, got %s", ct)
	}
	if !strings.Contains(w.Body.String(), "<svg") {
		t.Errorf("expected SVG body, got: %s", w.Body.String())
	}

	// Test /favicon.ico
	reqIco := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	wIco := httptest.NewRecorder()
	routes.ServeHTTP(wIco, reqIco)

	if wIco.Code != http.StatusOK {
		t.Errorf("expected 200 for /favicon.ico, got %d", wIco.Code)
	}
}

func TestHandleLogStreamValidation(t *testing.T) {
	srv, err := NewServer(config.DefaultConfig(), nil, nil)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	routes := srv.Routes()

	tests := []struct {
		name       string
		query      string
		wantStatus int
	}{
		{
			name:       "missing parameters",
			query:      "/logs/stream",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "container shell metacharacters",
			query:      "/logs/stream?server=app-server-01&container=app;id",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "container newline injection",
			query:      "/logs/stream?server=app-server-01&container=app%0aid",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "container flag injection",
			query:      "/logs/stream?server=app-server-01&container=--details",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "tail raw semicolon rejected",
			query:      "/logs/stream?server=app-server-01&container=my-app-web-1&tail=100;uname",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "tail encoded command injection",
			query:      "/logs/stream?server=app-server-01&container=my-app-web-1&tail=100%3Buname",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "tail subshell injection",
			query:      "/logs/stream?server=app-server-01&container=my-app-web-1&tail=100%20$(id)",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "tail newline injection",
			query:      "/logs/stream?server=app-server-01&container=my-app-web-1&tail=100%0auname",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "tail negative value",
			query:      "/logs/stream?server=app-server-01&container=my-app-web-1&tail=-5",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "tail exceeds max",
			query:      "/logs/stream?server=app-server-01&container=my-app-web-1&tail=999999",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "valid container and tail passes validation",
			query:      "/logs/stream?server=app-server-01&container=my_app.web-1&tail=150",
			wantStatus: http.StatusServiceUnavailable, // nil SSH pool after passing input validation
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.query, nil)
			w := httptest.NewRecorder()
			routes.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("GET %s: expected status %d, got %d (body: %q)", tc.query, tc.wantStatus, w.Code, w.Body.String())
			}
		})
	}
}
