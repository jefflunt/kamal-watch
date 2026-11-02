package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jefflunt/kamal-watch"
	"github.com/jefflunt/kamal-watch/config"
	"github.com/jefflunt/kamal-watch/pkg/models"
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

func TestHandleLogStream_PerServerConcurrencyCap(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MaxConcurrentLogStreams = 2

	srv, err := NewServer(cfg, nil, nil)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	started := make(chan struct{}, 4)
	releaseStreams := make(chan struct{})
	srv.streamFunc = func(ctx context.Context, serverName, cmd string, out io.Writer) error {
		started <- struct{}{}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-releaseStreams:
			return nil
		}
	}

	routes := srv.Routes()

	// Start 2 active streams on app-server-01 (filling the limit of 2)
	doneCh := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			req := httptest.NewRequest(http.MethodGet, "/logs/stream?server=app-server-01&container=web-1", nil)
			w := httptest.NewRecorder()
			routes.ServeHTTP(w, req)
			doneCh <- w.Code
		}()
	}

	<-started
	<-started

	// 3rd concurrent stream on app-server-01 must be rejected with 429 Too Many Requests
	req3 := httptest.NewRequest(http.MethodGet, "/logs/stream?server=app-server-01&container=web-1", nil)
	w3 := httptest.NewRecorder()
	routes.ServeHTTP(w3, req3)
	if w3.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 Too Many Requests when per-server cap is reached, got %d", w3.Code)
	}

	// Stream on a DIFFERENT server (app-server-02) must still be allowed
	close(releaseStreams)
	<-doneCh
	<-doneCh

	// After active streams finish, new stream on app-server-01 succeeds again
	reqAfter := httptest.NewRequest(http.MethodGet, "/logs/stream?server=app-server-01&container=web-1", nil)
	wAfter := httptest.NewRecorder()
	routes.ServeHTTP(wAfter, reqAfter)
	if wAfter.Code != http.StatusOK {
		t.Errorf("expected 200 OK after semaphore slot released, got %d", wAfter.Code)
	}
}

func TestTemplatesRenderHostMaintenanceBadges(t *testing.T) {
	srv, err := NewServer(config.DefaultConfig(), nil, nil)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	inv := &models.ServerInventory{
		ServerName: "prod-node-01",
		Host:       "10.0.0.10",
		Online:     true,
		Health: models.HostHealth{
			Uptime:             "up 14 days",
			LoadAvg:            "0.12, 0.08, 0.05",
			RebootRequired:     true,
			RebootRequiredPkgs: []string{"linux-image-generic", "libc6"},
			UpdatesAvailable:   5,
			SecurityUpdates:    1,
			UpdatesSummary:     "5 updates can be applied immediately. 1 of these updates is a standard security update.",
		},
		Apps: make(map[string]*models.KamalApp),
	}

	var partialBuf strings.Builder
	if err := srv.tmpl.ExecuteTemplate(&partialBuf, "servers_partial.html", DashboardData{
		Servers: []*models.ServerInventory{inv},
		Version: "0.3.0",
	}); err != nil {
		t.Fatalf("execute servers_partial.html: %v", err)
	}
	partialHTML := partialBuf.String()
	if !strings.Contains(partialHTML, "⟳ Reboot Required") {
		t.Errorf("expected servers_partial.html to contain '⟳ Reboot Required', got: %s", partialHTML)
	}
	if !strings.Contains(partialHTML, "linux-image-generic, libc6") {
		t.Errorf("expected servers_partial.html tooltip to list reboot packages, got: %s", partialHTML)
	}
	if !strings.Contains(partialHTML, "Updates: 5 (1 sec)") {
		t.Errorf("expected servers_partial.html to contain 'Updates: 5 (1 sec)', got: %s", partialHTML)
	}

	var serverBuf strings.Builder
	if err := srv.tmpl.ExecuteTemplate(&serverBuf, "server.html", inv); err != nil {
		t.Fatalf("execute server.html: %v", err)
	}
	serverHTML := serverBuf.String()
	if !strings.Contains(serverHTML, "⟳ Reboot Required") {
		t.Errorf("expected server.html to contain '⟳ Reboot Required', got: %s", serverHTML)
	}
	if !strings.Contains(serverHTML, "Updates: 5 (1 sec)") {
		t.Errorf("expected server.html to contain 'Updates: 5 (1 sec)', got: %s", serverHTML)
	}
}
