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
)

func main() {
	configPath := flag.String("config", "server-config.yaml", "path to config file")
	port := flag.Int("port", 0, "override server port")
	flag.Parse()

	// Load config
	cfg, err := Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if *port != 0 {
		cfg.Port = *port
	}

	log.Printf("SD Studio Server starting on port %d (backend: %s)", cfg.Port, cfg.ActiveSD)

	// Initialize components
	pm := NewProcessManager(cfg)
	proxy := NewProxyHandler(pm, cfg)
	hm := NewHealthMonitor(cfg)
	gm := NewGPUMonitor()
	mm := NewModelManager(cfg)
	inst := NewInstaller(cfg)
	bm := NewBackendManager(cfg, pm, inst)
	handlers := NewHandlers(pm, hm, gm, cfg, inst)

	// Setup HTTP mux
	mux := http.NewServeMux()

	// Register routes
	handlers.RegisterRoutes(mux)
	mm.RegisterRoutes(mux)
	mm.RegisterDeleteRoutes(mux)
	bm.RegisterRoutes(mux)
	inst.RegisterRoutes(mux)

	// Proxy catches /api/sd/*, /api/llm/*, /api/rembg/*
	mux.Handle("/api/sd/", proxy)
	mux.Handle("/api/llm/", proxy)
	mux.Handle("/api/rembg/", proxy)

	// Root endpoint
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			writeJSON(w, map[string]interface{}{
				"name":    "SD Studio Server",
				"version": "1.0",
				"status":  "running",
			})
			return
		}
		http.NotFound(w, r)
	})

	// Start context for background goroutines
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start auto-start processes
	pm.StartAll()

	// Start background monitors
	go pm.Watch(ctx)
	go hm.Start(ctx)
	go gm.Start(ctx)

	// Setup HTTP server
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      mux,
		ReadTimeout:  0, // SD uploads can be large
		WriteTimeout: 0, // SD generation can take minutes
	}

	// Start mDNS
	mdns := NewMDNS(cfg.Port, cfg.MDNS)
	if err := mdns.Register(); err != nil {
		log.Printf("mDNS registration failed (non-fatal): %v", err)
	} else if cfg.MDNS {
		log.Printf("mDNS: registered _sd-studio._tcp on port %d", cfg.Port)
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		cancel() // stop background goroutines

		srv.Shutdown(shutdownCtx)
		mdns.Shutdown()
		pm.StopAll()

		log.Println("Server stopped")
		os.Exit(0)
	}()

	log.Printf("HTTP server listening on :%d", cfg.Port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
