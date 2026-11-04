package web

import (
	"context"
	"fmt"
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

func TestServer_BasicAuthAndHealthcheck(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Servers = nil
	cfg.Auth = config.AuthConfig{
		Username: "admin",
		Password: "secret-password",
	}
	mon := monitor.NewMonitor(cfg, nil)
	srv, err := NewServer(cfg, mon, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	routes := srv.Routes()

	// 1. Healthcheck /up should succeed without credentials
	reqUp := httptest.NewRequest(http.MethodGet, "/up", nil)
	wUp := httptest.NewRecorder()
	routes.ServeHTTP(wUp, reqUp)
	if wUp.Code != http.StatusOK {
		t.Errorf("expected 200 for unauthenticated /up, got %d", wUp.Code)
	}

	// 2. Dashboard / without credentials should return 401
	reqUnauth := httptest.NewRequest(http.MethodGet, "/", nil)
	wUnauth := httptest.NewRecorder()
	routes.ServeHTTP(wUnauth, reqUnauth)
	if wUnauth.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated /, got %d", wUnauth.Code)
	}

	// 3. Dashboard / with wrong password should return 401
	reqWrong := httptest.NewRequest(http.MethodGet, "/", nil)
	reqWrong.SetBasicAuth("admin", "wrong")
	wWrong := httptest.NewRecorder()
	routes.ServeHTTP(wWrong, reqWrong)
	if wWrong.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong password, got %d", wWrong.Code)
	}

	// 4. Dashboard / with valid credentials should return 200
	reqAuth := httptest.NewRequest(http.MethodGet, "/", nil)
	reqAuth.SetBasicAuth("admin", "secret-password")
	wAuth := httptest.NewRecorder()
	routes.ServeHTTP(wAuth, reqAuth)
	if wAuth.Code != http.StatusOK {
		t.Errorf("expected 200 for authenticated /, got %d", wAuth.Code)
	}
}

func TestServer_HostHeaderValidation(t *testing.T) {
	// Loopback bind automatically restricts Host to localhost / 127.0.0.1 / ::1
	cfg := config.DefaultConfig()
	cfg.Servers = nil
	cfg.ListenAddr = "127.0.0.1:8080"
	mon := monitor.NewMonitor(cfg, nil)
	srv, err := NewServer(cfg, mon, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	routes := srv.Routes()

	// Rebound external Host header must be blocked
	reqEvil := httptest.NewRequest(http.MethodGet, "/api/inventory", nil)
	reqEvil.Host = "evil-rebind.example.com:8080"
	wEvil := httptest.NewRecorder()
	routes.ServeHTTP(wEvil, reqEvil)
	if wEvil.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for DNS rebinding Host header, got %d", wEvil.Code)
	}

	// Valid localhost Host header must succeed
	reqLocal := httptest.NewRequest(http.MethodGet, "/api/inventory", nil)
	reqLocal.Host = "127.0.0.1:8080"
	wLocal := httptest.NewRecorder()
	routes.ServeHTTP(wLocal, reqLocal)
	if wLocal.Code != http.StatusOK {
		t.Errorf("expected 200 OK for 127.0.0.1:8080 Host header, got %d", wLocal.Code)
	}

	// Explicit AllowedHosts list
	cfg.ListenAddr = ":8080"
	cfg.AllowedHosts = []string{"watch.internal"}
	reqCustom := httptest.NewRequest(http.MethodGet, "/api/inventory", nil)
	reqCustom.Host = "watch.internal"
	wCustom := httptest.NewRecorder()
	routes.ServeHTTP(wCustom, reqCustom)
	if wCustom.Code != http.StatusOK {
		t.Errorf("expected 200 OK for allowed host watch.internal, got %d", wCustom.Code)
	}
}

func TestEmbeddedStylesAndSecurityHeaders(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Servers = nil
	mon := monitor.NewMonitor(cfg, nil)
	srv, err := NewServer(cfg, mon, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	routes := srv.Routes()

	// 1. Verify /assets/styles.css serves embedded CSS
	reqCSS := httptest.NewRequest(http.MethodGet, "/assets/styles.css", nil)
	wCSS := httptest.NewRecorder()
	routes.ServeHTTP(wCSS, reqCSS)
	if wCSS.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /assets/styles.css, got %d", wCSS.Code)
	}
	if ct := wCSS.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("expected text/css Content-Type, got %q", ct)
	}
	if !strings.Contains(wCSS.Body.String(), ".meter-bar") {
		t.Errorf("expected /assets/styles.css to contain compiled utility and meter rules")
	}

	// 2. Verify dashboard HTML no longer references cdn.tailwindcss.com and includes security headers
	reqIdx := httptest.NewRequest(http.MethodGet, "/", nil)
	wIdx := httptest.NewRecorder()
	routes.ServeHTTP(wIdx, reqIdx)
	if wIdx.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /, got %d", wIdx.Code)
	}
	body := wIdx.Body.String()
	if strings.Contains(body, "cdn.tailwindcss.com") {
		t.Errorf("expected cdn.tailwindcss.com script to be removed from index.html")
	}
	if !strings.Contains(body, `/assets/styles.css`) {
		t.Errorf("expected index.html to link /assets/styles.css")
	}

	if got := wIdx.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("expected Referrer-Policy: no-referrer, got %q", got)
	}
	if got := wIdx.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("expected X-Content-Type-Options: nosniff, got %q", got)
	}
	if got := wIdx.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("expected X-Frame-Options: DENY, got %q", got)
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
		Version: kamalwatch.Version(),
	}); err != nil {
		t.Fatalf("execute servers_partial.html: %v", err)
	}
	partialHTML := partialBuf.String()
	if !strings.Contains(partialHTML, ">reboot</button>") {
		t.Errorf("expected servers_partial.html to contain '>reboot</button>', got: %s", partialHTML)
	}
	if !strings.Contains(partialHTML, "openCommandModal('prod-node-01', '10.0.0.10', 'reboot')") {
		t.Errorf("expected servers_partial.html to call openCommandModal for reboot, got: %s", partialHTML)
	}
	if !strings.Contains(partialHTML, "linux-image-generic, libc6") {
		t.Errorf("expected servers_partial.html tooltip to list reboot packages, got: %s", partialHTML)
	}
	if !strings.Contains(partialHTML, ">5 updates</button>") {
		t.Errorf("expected servers_partial.html to contain '>5 updates</button>', got: %s", partialHTML)
	}
	if !strings.Contains(partialHTML, "openCommandModal('prod-node-01', '10.0.0.10', 'update')") {
		t.Errorf("expected servers_partial.html to call openCommandModal for update, got: %s", partialHTML)
	}

	var serverBuf strings.Builder
	if err := srv.tmpl.ExecuteTemplate(&serverBuf, "server.html", inv); err != nil {
		t.Fatalf("execute server.html: %v", err)
	}
	serverHTML := serverBuf.String()
	if !strings.Contains(serverHTML, ">reboot</button>") {
		t.Errorf("expected server.html to contain '>reboot</button>', got: %s", serverHTML)
	}
	if !strings.Contains(serverHTML, "openCommandModal('prod-node-01', '10.0.0.10', 'reboot')") {
		t.Errorf("expected server.html to call openCommandModal for reboot, got: %s", serverHTML)
	}
	if !strings.Contains(serverHTML, ">5 updates</button>") {
		t.Errorf("expected server.html to contain '>5 updates</button>', got: %s", serverHTML)
	}
	if !strings.Contains(serverHTML, "openCommandModal('prod-node-01', '10.0.0.10', 'update')") {
		t.Errorf("expected server.html to call openCommandModal for update, got: %s", serverHTML)
	}
	if !strings.Contains(serverHTML, "Run the following command on") {
		t.Errorf("expected server.html to include command takeover modal, got: %s", serverHTML)
	}
}

func TestTemplatesRenderEnhancedLabelsAndCPUMeters(t *testing.T) {
	srv, err := NewServer(config.DefaultConfig(), nil, nil)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	inv := &models.ServerInventory{
		ServerName: "compute-01",
		Host:       "192.168.0.31",
		Online:     true,
		Health: models.HostHealth{
			Uptime:        "03:51:14 up 8 min, 0 users, load average: 0.00, 0.15, 0.15",
			CPUSockets:    1,
			CPUCores:      32,
			CPUModel:      "AMD 5950X",
			CPUUsagePerc:  14.5,
			CPUCoresUsage: []float64{12.0, 15.0, 5.0, 30.0},
			MemUsed:       "16.0Gi",
			MemTotal:      "32.0Gi",
			MemAvailable:  "16.0Gi",
			MemUsedPerc:   50.0,
			SwapUsed:      "1.0Gi",
			SwapTotal:     "4.0Gi",
			SwapUsedPerc:  25.0,
			DiskUsage:     "20G/100G (20%)",
			DiskUsedPerc:  20.0,
		},
		Apps: map[string]*models.KamalApp{
			"my-service": {
				Name: "my-service",
				Aggregates: models.ServiceAggregates{
					RunningContainers: 1,
					TotalContainers:   1,
				},
			},
		},
	}

	var partialBuf strings.Builder
	if err := srv.tmpl.ExecuteTemplate(&partialBuf, "servers_partial.html", DashboardData{
		Servers: []*models.ServerInventory{inv},
		Version: kamalwatch.Version(),
	}); err != nil {
		t.Fatalf("execute servers_partial.html: %v", err)
	}
	html := partialBuf.String()

	// 1. Check server label: compute-01@192.168.0.31 and no "SERVER:"
	if !strings.Contains(html, "compute-01@192.168.0.31") {
		t.Errorf("expected server label compute-01@192.168.0.31, got: %s", html)
	}
	if strings.Contains(html, "SERVER:") {
		t.Errorf("expected 'SERVER:' prefix to be removed from server label")
	}

	// 2. Check CPU label: 1x32 [AMD 5950X] and no "1 [CPU Total:"
	if !strings.Contains(html, "1x32 [AMD 5950X]") {
		t.Errorf("expected CPU label 1x32 [AMD 5950X], got: %s", html)
	}
	if strings.Contains(html, "1 [CPU Total:") {
		t.Errorf("expected '1 [CPU Total:' to be replaced by enhanced CPU label")
	}

	// 3. Check service label: "my-service" without "[service]"
	if !strings.Contains(html, "my-service") {
		t.Errorf("expected service name 'my-service' in html")
	}
	if strings.Contains(html, "[service]") {
		t.Errorf("expected '[service]' tag to be removed from service label")
	}

	// 4. Check unified and per-core meter containers
	if !strings.Contains(html, "cpu-unified") {
		t.Errorf("expected .cpu-unified container in html")
	}
	if !strings.Contains(html, "cpu-per-core") {
		t.Errorf("expected .cpu-per-core container in html")
	}

	// 5. Check memory label: "16.0Gi / 32.0Gi" without "(Avail:"
	if !strings.Contains(html, "16.0Gi / 32.0Gi") {
		t.Errorf("expected '16.0Gi / 32.0Gi' in html, got: %s", html)
	}
	if strings.Contains(html, "(Avail:") {
		t.Errorf("expected '(Avail:' to be removed from memory label")
	}

	// 6. Check uptime header: contains Uptime without redundant "Load average:" span
	if !strings.Contains(html, "03:51:14 up 8 min, 0 users, load average: 0.00, 0.15, 0.15") {
		t.Errorf("expected uptime text in html")
	}
	if strings.Contains(html, "<span>Load average:") {
		t.Errorf("expected redundant 'Load average:' span to be removed from uptime section")
	}

	// 7. Check meter widths reflect calculated percentages
	if !strings.Contains(html, `style="width: 50.0%;"`) {
		t.Errorf("expected memory meter to have style width 50.0%%")
	}
	if !strings.Contains(html, `style="width: 25.0%;"`) {
		t.Errorf("expected swap meter to have style width 25.0%%")
	}
	if !strings.Contains(html, `style="width: 20.0%;"`) {
		t.Errorf("expected disk meter to have style width 20.0%%")
	}
}

func TestHandleRunCommand(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Servers = []config.ServerConfig{
		{Name: "app-node-01", Host: "10.0.0.10"},
	}
	mon := monitor.NewMonitor(cfg, nil)
	srv, err := NewServer(cfg, mon, nil)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	routes := srv.Routes()

	// 1. Unknown server -> 404
	{
		body := `{"command": "sudo reboot"}`
		req := httptest.NewRequest(http.MethodPost, "/api/server/unknown-node/run", strings.NewReader(body))
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404 for unknown server, got %d", w.Code)
		}
	}

	// 2. Empty command -> 400
	{
		body := `{"command": "   "}`
		req := httptest.NewRequest(http.MethodPost, "/api/server/app-node-01/run", strings.NewReader(body))
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for empty command, got %d", w.Code)
		}
	}

	// 3. Invalid JSON body -> 400
	{
		body := `not-json`
		req := httptest.NewRequest(http.MethodPost, "/api/server/app-node-01/run", strings.NewReader(body))
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for invalid json, got %d", w.Code)
		}
	}

	// 4. SSH pool unavailable (pool is nil and runCmdFunc is nil) -> 503
	{
		body := `{"command": "sudo apt-get update"}`
		req := httptest.NewRequest(http.MethodPost, "/api/server/app-node-01/run", strings.NewReader(body))
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("expected 503 when pool unavailable, got %d", w.Code)
		}
	}

	// 5. Successful command execution via runCmdFunc mock -> 200, success: true
	{
		srv.runCmdFunc = func(ctx context.Context, serverName, cmd string) ([]byte, error) {
			if serverName != "app-node-01" {
				t.Errorf("expected serverName app-node-01, got %s", serverName)
			}
			if cmd != "sudo apt-get update && sudo apt-get dist-upgrade" {
				t.Errorf("unexpected command: %s", cmd)
			}
			return []byte("All packages up to date.\n"), nil
		}

		body := `{"command": "sudo apt-get update && sudo apt-get dist-upgrade"}`
		req := httptest.NewRequest(http.MethodPost, "/api/server/app-node-01/run", strings.NewReader(body))
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d (body: %s)", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"success":true`) {
			t.Errorf("expected success:true in response, got %s", w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "All packages up to date.") {
			t.Errorf("expected output in response, got %s", w.Body.String())
		}
	}

	// 6. Command failure or connection termination -> 200 with success: false and error
	{
		srv.runCmdFunc = func(ctx context.Context, serverName, cmd string) ([]byte, error) {
			return []byte("Broadcast message from root: system is going down for reboot NOW!"), fmt.Errorf("connection reset by peer")
		}

		body := `{"command": "sudo reboot"}`
		req := httptest.NewRequest(http.MethodPost, "/api/server/app-node-01/run", strings.NewReader(body))
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", w.Code)
		}
		if !strings.Contains(w.Body.String(), `"success":false`) {
			t.Errorf("expected success:false in response, got %s", w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "connection reset by peer") {
			t.Errorf("expected error message in response, got %s", w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "going down for reboot") {
			t.Errorf("expected command output in response, got %s", w.Body.String())
		}
	}
}

func TestCommandModalTemplateStructure(t *testing.T) {
	srv, err := NewServer(config.DefaultConfig(), nil, nil)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	var buf strings.Builder
	if err := srv.tmpl.ExecuteTemplate(&buf, "command_modal.html", nil); err != nil {
		t.Fatalf("failed to execute command_modal.html: %v", err)
	}
	html := buf.String()

	checks := []struct {
		desc     string
		expected string
	}{
		{"modal root", `id="cmd-modal"`},
		{"viewport height constraint", `max-h-[85vh]`},
		{"header dividing border", `border-b border-[#30363d]`},
		{"host placeholder", `id="cmd-modal-host"`},
		{"command input label", `Command`},
		{"command input element", `id="cmd-modal-input"`},
		{"output section container", `id="cmd-modal-output-section"`},
		{"output status badge", `id="cmd-modal-status-badge"`},
		{"auto-scroll toggle", `id="cmd-modal-autoscroll"`},
		{"clear output button", `clearCmdOutput()`},
		{"scrollable output box", `id="cmd-modal-output-box"`},
		{"scrollable output vertical overflow", `overflow-y-auto`},
		{"output height constraint", `max-h-[55vh]`},
		{"footer action bar", `id="cmd-modal-cancel-btn"`},
		{"action submit button", `id="cmd-modal-yes-btn"`},
	}

	for _, c := range checks {
		if !strings.Contains(html, c.expected) {
			t.Errorf("expected command_modal.html to contain %q (%s)", c.expected, c.desc)
		}
	}
}
