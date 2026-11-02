package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jefflunt/kamal-watch/config"
	"github.com/jefflunt/kamal-watch/pkg/monitor"
	"github.com/jefflunt/kamal-watch/pkg/remote"
	"github.com/jefflunt/kamal-watch/pkg/web"
)

func main() {
	configPath := flag.String("config", "kamal-watch.yml", "Path to configuration file")
	addr := flag.String("addr", "", "Override HTTP listen address (e.g. :8080)")
	flag.Parse()

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	if *addr != "" {
		cfg.ListenAddr = *addr
	}

	log.Printf("Starting Kamal Watch...")
	log.Printf("Configured %d servers to monitor", len(cfg.Servers))

	pool := remote.NewSSHPool(cfg.Servers)
	defer pool.Close()
	mon := monitor.NewMonitor(cfg, pool)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start background polling
	mon.Start(ctx)

	server, err := web.NewServer(cfg, mon, pool)
	if err != nil {
		log.Fatalf("Failed to initialize web server: %v", err)
	}

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("Kamal Watch Web App listening on http://localhost%s", cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down gracefully...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
	fmt.Println("Kamal Watch stopped.")
}
